// Package redis provides a Redis-based data source for dynamic routing configuration.
package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(&RedisSource{})
}

// RedisSource implements datasource.DataSource using Redis as the backend.
type RedisSource struct {
	// Addresses is the list of Redis server addresses.
	// For standalone: single address like "localhost:6379"
	// For cluster: multiple addresses
	Addresses []string `json:"addresses,omitempty"`

	// Password for Redis authentication.
	Password string `json:"password,omitempty"`

	// DB is the Redis database number (standalone mode only).
	DB int `json:"db,omitempty"`

	// Prefix is the key prefix for routing configurations.
	// Default: "caddy:routing:"
	Prefix string `json:"prefix,omitempty"`

	// DialTimeout is the timeout for establishing connection.
	// Default: 5s
	DialTimeout caddy.Duration `json:"dial_timeout,omitempty"`

	// ReadTimeout is the timeout for read operations.
	// Default: 2s
	ReadTimeout caddy.Duration `json:"read_timeout,omitempty"`

	// Cluster enables Redis Cluster mode.
	Cluster bool `json:"cluster,omitempty"`

	// Sentinel configuration for high availability.
	Sentinel *SentinelConfig `json:"sentinel,omitempty"`

	// TLS configuration
	TLSEnabled    bool `json:"tls_enabled,omitempty"`
	TLSSkipVerify bool `json:"tls_skip_verify,omitempty"`

	// PubSubChannel is the channel name for routing update notifications.
	// Default: "caddy:routing:updates"
	PubSubChannel string `json:"pubsub_channel,omitempty"`

	// KeyspaceNotify enables Redis Keyspace Notifications instead of custom PubSub.
	// Requires Redis configured with: notify-keyspace-events Ksh
	KeyspaceNotify bool `json:"keyspace_notify,omitempty"`

	// Connection pool configuration
	// PoolSize is the maximum number of socket connections.
	// Default: 10 * runtime.GOMAXPROCS
	PoolSize int `json:"pool_size,omitempty"`

	// MinIdleConns is the minimum number of idle connections.
	// Default: 0
	MinIdleConns int `json:"min_idle_conns,omitempty"`

	// MaxIdleConns is the maximum number of idle connections.
	// Default: PoolSize
	MaxIdleConns int `json:"max_idle_conns,omitempty"`

	// PoolTimeout is the amount of time client waits for connection if all
	// connections are busy before returning an error.
	// Default: ReadTimeout + 1 second
	PoolTimeout caddy.Duration `json:"pool_timeout,omitempty"`

	// ConnMaxIdleTime is the maximum amount of time a connection may be idle.
	// Default: 30 minutes
	ConnMaxIdleTime caddy.Duration `json:"conn_max_idle_time,omitempty"`

	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	// Default: 0 (no limit)
	ConnMaxLifetime caddy.Duration `json:"conn_max_lifetime,omitempty"`

	// MaxCacheSize is the maximum number of entries in the cache.
	// Default: 10000
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// InitialLoadTimeout is the maximum time allowed for the initial load during
	// Provision.
	// Default: DialTimeout
	InitialLoadTimeout caddy.Duration `json:"initial_load_timeout,omitempty"`

	// Internal state
	client          redis.UniversalClient
	cache           *cache.RouteCache
	healthy         atomic.Bool
	logger          *zap.Logger
	cancelPubSub    context.CancelFunc
	sfGroup         singleflight.Group // Coalesce concurrent requests for same key
	poolCollector   *metrics.PoolStatsCollector
	cancelPoolStats context.CancelFunc

	// Admin / diagnostics
	adminName string
	lastError atomic.Value // string
}

// SentinelConfig holds Redis Sentinel configuration.
type SentinelConfig struct {
	// MasterName is the name of the master to connect to.
	MasterName string `json:"master_name,omitempty"`

	// SentinelAddresses is the list of Sentinel addresses.
	SentinelAddresses []string `json:"addresses,omitempty"`

	// SentinelPassword is the password for Sentinel authentication.
	SentinelPassword string `json:"password,omitempty"`
}

// CaddyModule returns the Caddy module information.
func (*RedisSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.redis",
		New: func() caddy.Module { return new(RedisSource) },
	}
}

// Provision sets up the Redis data source.
func (r *RedisSource) Provision(ctx caddy.Context) error {
	r.logger = ctx.Logger()

	// Set defaults
	if r.Prefix == "" {
		r.Prefix = "caddy:routing:"
	}
	if r.DialTimeout == 0 {
		r.DialTimeout = caddy.Duration(5 * time.Second)
	}
	if r.ReadTimeout == 0 {
		r.ReadTimeout = caddy.Duration(2 * time.Second)
	}
	if r.PubSubChannel == "" {
		r.PubSubChannel = "caddy:routing:updates"
	}

	// Initialize LRU cache with negative caching support
	r.cache = cache.NewRouteCache(r.MaxCacheSize)

	// Create Redis client
	r.client = r.createClient()

	// Initial health check and data load
	loadCtx := ctx.Context
	if r.InitialLoadTimeout != 0 {
		c, cancel := context.WithTimeout(ctx.Context, time.Duration(r.InitialLoadTimeout))
		defer cancel()
		loadCtx = c
	}
	if err := r.initialLoad(loadCtx); err != nil {
		r.logger.Warn("initial Redis load failed, will retry", zap.Error(err))
		r.lastError.Store(err.Error())
		r.healthy.Store(false)
	} else {
		r.healthy.Store(true)
		r.lastError.Store("")
	}

	// Start PubSub subscriber for real-time updates
	pubsubCtx, cancel := context.WithCancel(ctx.Context)
	r.cancelPubSub = cancel
	go r.subscribeLoop(pubsubCtx)

	// Start pool stats collection
	r.startPoolStatsCollection()

	r.logger.Info("Redis data source provisioned",
		zap.Strings("addresses", r.Addresses),
		zap.String("prefix", r.Prefix),
		zap.Bool("cluster", r.Cluster),
		zap.Int("max_cache_size", r.MaxCacheSize),
	)

	// Register for Admin API inspection
	r.adminName = datasource.RegisterAdminSource(r)

	return nil
}

// createClient creates the appropriate Redis client based on configuration.
func (r *RedisSource) createClient() redis.UniversalClient {
	var tlsConfig *tls.Config
	if r.TLSEnabled {
		tlsConfig = &tls.Config{
			// #nosec G402 -- configurable for dev/test environments; default is secure verification.
			InsecureSkipVerify: r.TLSSkipVerify,
		}
	}

	// Sentinel mode
	if r.Sentinel != nil && r.Sentinel.MasterName != "" {
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       r.Sentinel.MasterName,
			SentinelAddrs:    r.Sentinel.SentinelAddresses,
			SentinelPassword: r.Sentinel.SentinelPassword,
			Password:         r.Password,
			DB:               r.DB,
			DialTimeout:      time.Duration(r.DialTimeout),
			ReadTimeout:      time.Duration(r.ReadTimeout),
			TLSConfig:        tlsConfig,
			// Pool configuration
			PoolSize:        r.PoolSize,
			MinIdleConns:    r.MinIdleConns,
			MaxIdleConns:    r.MaxIdleConns,
			PoolTimeout:     time.Duration(r.PoolTimeout),
			ConnMaxIdleTime: time.Duration(r.ConnMaxIdleTime),
			ConnMaxLifetime: time.Duration(r.ConnMaxLifetime),
		})
	}

	// Cluster mode
	if r.Cluster {
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:       r.Addresses,
			Password:    r.Password,
			DialTimeout: time.Duration(r.DialTimeout),
			ReadTimeout: time.Duration(r.ReadTimeout),
			TLSConfig:   tlsConfig,
			// Pool configuration
			PoolSize:        r.PoolSize,
			MinIdleConns:    r.MinIdleConns,
			MaxIdleConns:    r.MaxIdleConns,
			PoolTimeout:     time.Duration(r.PoolTimeout),
			ConnMaxIdleTime: time.Duration(r.ConnMaxIdleTime),
			ConnMaxLifetime: time.Duration(r.ConnMaxLifetime),
		})
	}

	// Standalone mode
	addr := "localhost:6379"
	if len(r.Addresses) > 0 {
		addr = r.Addresses[0]
	}
	return redis.NewClient(&redis.Options{
		Addr:        addr,
		Password:    r.Password,
		DB:          r.DB,
		DialTimeout: time.Duration(r.DialTimeout),
		ReadTimeout: time.Duration(r.ReadTimeout),
		TLSConfig:   tlsConfig,
		// Pool configuration
		PoolSize:        r.PoolSize,
		MinIdleConns:    r.MinIdleConns,
		MaxIdleConns:    r.MaxIdleConns,
		PoolTimeout:     time.Duration(r.PoolTimeout),
		ConnMaxIdleTime: time.Duration(r.ConnMaxIdleTime),
		ConnMaxLifetime: time.Duration(r.ConnMaxLifetime),
	})
}

// Cleanup releases resources.
func (r *RedisSource) Cleanup() error {
	datasource.UnregisterAdminSource(r.adminName)
	r.adminName = ""

	if r.cancelPoolStats != nil {
		r.cancelPoolStats()
	}
	if r.cancelPubSub != nil {
		r.cancelPubSub()
	}

	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// startPoolStatsCollection starts a goroutine to periodically collect pool stats.
func (r *RedisSource) startPoolStatsCollection() {
	// Build instance identifier from addresses
	instance := "redis"
	if len(r.Addresses) > 0 {
		instance = strings.Join(r.Addresses, ",")
	}
	if r.Sentinel != nil && r.Sentinel.MasterName != "" {
		instance = "sentinel:" + r.Sentinel.MasterName
	}

	r.poolCollector = metrics.NewPoolStatsCollector("redis", instance)

	ctx, cancel := context.WithCancel(context.Background())
	r.cancelPoolStats = cancel

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.collectPoolStats()
			}
		}
	}()
}

// collectPoolStats collects and exports pool statistics.
func (r *RedisSource) collectPoolStats() {
	if r.client == nil || r.poolCollector == nil {
		return
	}

	stats := r.client.PoolStats()
	if stats == nil {
		return
	}

	r.poolCollector.Update(metrics.PoolStats{
		Hits:       stats.Hits,
		Misses:     stats.Misses,
		Timeouts:   stats.Timeouts,
		TotalConns: stats.TotalConns,
		IdleConns:  stats.IdleConns,
		StaleConns: stats.StaleConns,
	})
}

// Get retrieves the route configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (r *RedisSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	fullKey := r.Prefix + key

	// Try cache first
	if cached := r.cache.Get(fullKey); cached != nil {
		return cached, nil
	}

	// Check negative cache
	if r.cache.IsNegativeCached(fullKey) {
		return nil, nil
	}

	// If unhealthy, don't try to fetch from Redis
	if !r.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := r.sfGroup.Do(fullKey, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if cached := r.cache.Get(fullKey); cached != nil {
			return cached, nil
		}

		// Fetch from Redis with timeout
		reqCtx, cancel := context.WithTimeout(ctx, time.Duration(r.ReadTimeout))
		defer cancel()

		val, err := r.client.Get(reqCtx, fullKey).Result()
		if err == redis.Nil {
			// Cache negative result to avoid repeated lookups for missing keys
			r.cache.SetNegative(fullKey)
			return nil, nil
		}
		if err != nil {
			r.logger.Warn("Redis get failed", zap.String("key", fullKey), zap.Error(err))
			r.lastError.Store(err.Error())
			return nil, err
		}

		// Parse and cache the config
		config, err := datasource.ParseRouteConfig([]byte(val))
		if err != nil {
			metrics.RecordRouteConfigParseError(r.AdminType())
			r.logger.Error("failed to parse route config", zap.String("key", fullKey), zap.Error(err))
			r.lastError.Store(err.Error())
			return nil, err
		}

		if config != nil {
			r.cache.Set(fullKey, config)
		}

		return config, nil
	})

	if err != nil {
		return nil, err
	}
	r.lastError.Store("")
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// AdminType returns the short type name for Admin API.
func (*RedisSource) AdminType() string { return "redis" }

// AdminLastError returns the most recent error message (best-effort).
func (r *RedisSource) AdminLastError() string {
	if v := r.lastError.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AdminListRoutes returns a snapshot of cached routes.
func (r *RedisSource) AdminListRoutes() []datasource.AdminRouteInfo {
	if r.cache == nil {
		return nil
	}

	routes := make([]datasource.AdminRouteInfo, 0, r.cache.Len())
	r.cache.Range(func(k string, cfg *datasource.RouteConfig) bool {
		key := strings.TrimPrefix(k, r.Prefix)
		routes = append(routes, datasource.SummarizeRouteConfig(key, cfg))
		return true
	})
	return routes
}

// AdminCacheStats returns a snapshot of internal cache stats.
func (r *RedisSource) AdminCacheStats() datasource.AdminCacheStats {
	if r.cache == nil {
		return datasource.AdminCacheStats{}
	}
	h, m, nh, hr := r.cache.Stats()
	return datasource.AdminCacheStats{
		Entries:      r.cache.Len(),
		MaxSize:      r.cache.MaxSize(),
		Hits:         h,
		Misses:       m,
		NegativeHits: nh,
		HitRate:      hr,
	}
}

// AdminClearCache clears all internal caches.
func (r *RedisSource) AdminClearCache() {
	if r.cache != nil {
		r.cache.Clear()
	}
}

// Healthy returns true if the Redis connection is healthy.
func (r *RedisSource) Healthy() bool {
	return r.healthy.Load()
}

// initialLoad loads all existing routing configurations from Redis.
func (r *RedisSource) initialLoad(ctx context.Context) error {
	timeout := time.Duration(r.DialTimeout)
	if r.InitialLoadTimeout != 0 {
		timeout = time.Duration(r.InitialLoadTimeout)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Ping to verify connection
	if err := r.client.Ping(reqCtx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %v", err)
	}

	// Scan for all keys with prefix
	var cursor uint64
	var count int
	for {
		keys, nextCursor, err := r.client.Scan(reqCtx, cursor, r.Prefix+"*", 100).Result()
		if err != nil {
			return fmt.Errorf("redis scan failed: %v", err)
		}

		if len(keys) > 0 {
			// Get all values in batch
			vals, err := r.client.MGet(reqCtx, keys...).Result()
			if err != nil {
				r.logger.Warn("Redis mget failed during initial load", zap.Error(err))
			} else {
				for i, val := range vals {
					if val == nil {
						continue
					}
					strVal, ok := val.(string)
					if !ok {
						continue
					}
					config, err := datasource.ParseRouteConfig([]byte(strVal))
					if err != nil {
						metrics.RecordRouteConfigParseError(r.AdminType())
						r.logger.Warn("failed to parse config during initial load",
							zap.String("key", keys[i]),
							zap.Error(err),
						)
						continue
					}
					if config != nil {
						r.cache.Set(keys[i], config)
						count++
					}
				}
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	r.logger.Info("initial Redis load completed", zap.Int("count", count))
	return nil
}

// subscribeLoop continuously subscribes to routing updates.
func (r *RedisSource) subscribeLoop(ctx context.Context) {
	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := r.subscribe(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logger.Warn("subscribe error, will retry", zap.Error(err), zap.Duration("backoff", backoff))
			r.healthy.Store(false)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			// Exponential backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = time.Second
		}
	}
}

// subscribe subscribes to routing updates via PubSub or Keyspace Notifications.
func (r *RedisSource) subscribe(ctx context.Context) error {
	var pubsub *redis.PubSub

	if r.KeyspaceNotify {
		// Subscribe to keyspace notifications
		// In cluster mode, DB is always 0
		db := r.DB
		if r.Cluster {
			db = 0
		}
		pattern := fmt.Sprintf("__keyspace@%d__:%s*", db, r.Prefix)
		pubsub = r.client.PSubscribe(ctx, pattern)
	} else {
		// Subscribe to custom channel
		pubsub = r.client.Subscribe(ctx, r.PubSubChannel)
	}
	defer func() {
		if err := pubsub.Close(); err != nil {
			r.logger.Debug("failed to close pubsub", zap.Error(err))
		}
	}()

	// Confirm subscription
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return fmt.Errorf("subscription failed: %v", err)
	}

	r.healthy.Store(true)
	r.logger.Debug("Redis subscription started",
		zap.String("channel", r.PubSubChannel),
		zap.Bool("keyspace_notify", r.KeyspaceNotify),
	)

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("subscription channel closed")
			}
			r.handleMessage(ctx, msg)
		}
	}
}

// handleMessage processes incoming PubSub messages.
func (r *RedisSource) handleMessage(ctx context.Context, msg *redis.Message) {
	if r.KeyspaceNotify {
		// Keyspace notification format: channel = __keyspace@0__:caddy:routing:tenant-a
		// Extract key from channel name
		// In cluster mode, DB is always 0
		db := r.DB
		if r.Cluster {
			db = 0
		}
		key := msg.Channel[len(fmt.Sprintf("__keyspace@%d__:", db)):]

		switch msg.Payload {
		case "set", "hset":
			r.refreshKey(ctx, key)
		case "del", "expired":
			r.cache.Delete(key)
			r.logger.Info("route config deleted", zap.String("key", key))
		}
	} else {
		// Custom PubSub format: payload = "update:caddy:routing:tenant-a" or "delete:caddy:routing:tenant-a"
		if key, ok := strings.CutPrefix(msg.Payload, "update:"); ok {
			r.refreshKey(ctx, key)
			return
		}
		if key, ok := strings.CutPrefix(msg.Payload, "delete:"); ok {
			r.cache.Delete(key)
			r.logger.Info("route config deleted", zap.String("key", key))
			return
		}
	}
}

// refreshKey fetches and caches the latest value for a key.
func (r *RedisSource) refreshKey(ctx context.Context, key string) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(r.ReadTimeout))
	defer cancel()

	val, err := r.client.Get(reqCtx, key).Result()
	if err == redis.Nil {
		r.cache.Delete(key)
		return
	}
	if err != nil {
		r.logger.Warn("failed to refresh key", zap.String("key", key), zap.Error(err))
		return
	}

	config, err := datasource.ParseRouteConfig([]byte(val))
	if err != nil {
		metrics.RecordRouteConfigParseError(r.AdminType())
		r.logger.Warn("failed to parse refreshed config", zap.String("key", key), zap.Error(err))
		return
	}

	if config != nil {
		r.cache.Set(key, config)
		r.logger.Info("route config updated", zap.String("key", key))
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (r *RedisSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	parseDurationArg := func(name string) (caddy.Duration, error) {
		if !d.NextArg() {
			return 0, d.ArgErr()
		}
		dur, err := caddy.ParseDuration(d.Val())
		if err != nil {
			return 0, d.Errf("invalid %s: %v", name, err)
		}
		return caddy.Duration(dur), nil
	}

	parseIntArg := func(name string) (int, error) {
		if !d.NextArg() {
			return 0, d.ArgErr()
		}
		var n int
		if _, err := fmt.Sscanf(d.Val(), "%d", &n); err != nil {
			return 0, d.Errf("invalid %s: %v", name, err)
		}
		return n, nil
	}

	for d.Next() {
		handlers := map[string]func() error{
			"addresses": func() error {
				r.Addresses = d.RemainingArgs()
				if len(r.Addresses) == 0 {
					return d.ArgErr()
				}
				return nil
			},
			"addrs": func() error {
				r.Addresses = d.RemainingArgs()
				if len(r.Addresses) == 0 {
					return d.ArgErr()
				}
				return nil
			},
			"addr": func() error {
				if !d.NextArg() {
					return d.ArgErr()
				}
				r.Addresses = []string{d.Val()}
				return nil
			},
			"password": func() error {
				if !d.NextArg() {
					return d.ArgErr()
				}
				r.Password = d.Val()
				return nil
			},
			"db": func() error {
				n, err := parseIntArg("db")
				if err != nil {
					return err
				}
				r.DB = n
				return nil
			},
			"prefix": func() error {
				if !d.NextArg() {
					return d.ArgErr()
				}
				r.Prefix = d.Val()
				return nil
			},
			"dial_timeout": func() error {
				dur, err := parseDurationArg("dial_timeout")
				if err != nil {
					return err
				}
				r.DialTimeout = dur
				return nil
			},
			"read_timeout": func() error {
				dur, err := parseDurationArg("read_timeout")
				if err != nil {
					return err
				}
				r.ReadTimeout = dur
				return nil
			},
			"initial_load_timeout": func() error {
				dur, err := parseDurationArg("initial_load_timeout")
				if err != nil {
					return err
				}
				r.InitialLoadTimeout = dur
				return nil
			},
			"cluster": func() error {
				if args := d.RemainingArgs(); len(args) != 0 {
					return d.ArgErr()
				}
				r.Cluster = true
				return nil
			},
			"sentinel": func() error {
				r.Sentinel = &SentinelConfig{}
				for nesting := d.Nesting(); d.NextBlock(nesting); {
					switch d.Val() {
					case "master":
						if !d.NextArg() {
							return d.ArgErr()
						}
						r.Sentinel.MasterName = d.Val()
					case "addresses":
						r.Sentinel.SentinelAddresses = d.RemainingArgs()
						if len(r.Sentinel.SentinelAddresses) == 0 {
							return d.ArgErr()
						}
					case "password":
						if !d.NextArg() {
							return d.ArgErr()
						}
						r.Sentinel.SentinelPassword = d.Val()
					default:
						return d.Errf("unknown sentinel subdirective: %s", d.Val())
					}
				}
				return nil
			},
			"tls": func() error {
				r.TLSEnabled = true
				for nesting := d.Nesting(); d.NextBlock(nesting); {
					switch d.Val() {
					case "skip_verify":
						r.TLSSkipVerify = true
					default:
						return d.Errf("unknown tls subdirective: %s", d.Val())
					}
				}
				return nil
			},
			"pubsub_channel": func() error {
				if !d.NextArg() {
					return d.ArgErr()
				}
				r.PubSubChannel = d.Val()
				return nil
			},
			"keyspace_notify": func() error {
				if args := d.RemainingArgs(); len(args) != 0 {
					return d.ArgErr()
				}
				r.KeyspaceNotify = true
				return nil
			},
		}

		for nesting := d.Nesting(); d.NextBlock(nesting); {
			name := d.Val()
			h, ok := handlers[name]
			if !ok {
				return d.Errf("unknown subdirective: %s", name)
			}
			if err := h(); err != nil {
				return err
			}
		}
	}

	return nil
}

// Validate implements caddy.Validator.
func (r *RedisSource) Validate() error {
	if len(r.Addresses) == 0 && r.Sentinel == nil {
		return fmt.Errorf("at least one address is required (or use sentinel configuration)")
	}
	if r.DB < 0 || r.DB > 15 {
		return fmt.Errorf("db must be between 0 and 15")
	}
	if r.DialTimeout < 0 {
		return fmt.Errorf("dial_timeout must be non-negative")
	}
	if r.ReadTimeout < 0 {
		return fmt.Errorf("read_timeout must be non-negative")
	}
	if r.Cluster && r.DB != 0 {
		return fmt.Errorf("db selection is not supported in cluster mode")
	}
	if r.Sentinel != nil {
		if r.Sentinel.MasterName == "" {
			return fmt.Errorf("sentinel master_name is required")
		}
		if len(r.Sentinel.SentinelAddresses) == 0 {
			return fmt.Errorf("at least one sentinel address is required")
		}
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*RedisSource)(nil)
	_ caddy.Provisioner     = (*RedisSource)(nil)
	_ caddy.CleanerUpper    = (*RedisSource)(nil)
	_ caddy.Validator       = (*RedisSource)(nil)
	_ datasource.DataSource = (*RedisSource)(nil)
	_ caddyfile.Unmarshaler = (*RedisSource)(nil)
)
