// Package consul provides a Consul-based data source for dynamic routing configuration.
package consul

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/hashicorp/consul/api"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(&ConsulSource{})
}

// ConsulSource implements datasource.DataSource using Consul KV as the backend.
type ConsulSource struct {
	// Address is the Consul agent address.
	// Default: "localhost:8500"
	Address string `json:"address,omitempty"`

	// Scheme is the URI scheme (http or https).
	// Default: "http"
	Scheme string `json:"scheme,omitempty"`

	// Datacenter is the datacenter to query.
	Datacenter string `json:"datacenter,omitempty"`

	// Token is the ACL token for authentication.
	Token string `json:"token,omitempty"`

	// Prefix is the key prefix for routing configurations.
	// Default: "caddy/routing/"
	Prefix string `json:"prefix,omitempty"`

	// Namespace is the Consul namespace (Enterprise only).
	Namespace string `json:"namespace,omitempty"`

	// WaitTime is the maximum duration for blocking queries.
	// Default: 5m
	WaitTime caddy.Duration `json:"wait_time,omitempty"`

	// TLS configuration
	TLSEnabled    bool   `json:"tls_enabled,omitempty"`
	TLSCertFile   string `json:"tls_cert_file,omitempty"`
	TLSKeyFile    string `json:"tls_key_file,omitempty"`
	TLSCAFile     string `json:"tls_ca_file,omitempty"`
	TLSSkipVerify bool   `json:"tls_skip_verify,omitempty"`

	// MaxCacheSize is the maximum number of entries in the cache.
	// Default: 10000
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// InitialLoadTimeout is the maximum time allowed for the initial load during
	// Provision.
	// Default: 30s
	InitialLoadTimeout caddy.Duration `json:"initial_load_timeout,omitempty"`

	// Internal state
	client      *api.Client
	cache       *cache.RouteCache
	healthy     atomic.Bool
	logger      *zap.Logger
	cancelWatch context.CancelFunc
	watchWg     sync.WaitGroup
	lastIndex   atomic.Uint64
	sfGroup     singleflight.Group // Coalesce concurrent requests for same key

	// Admin / diagnostics
	adminName string
	lastError atomic.Value // string
}

// CaddyModule returns the Caddy module information.
func (*ConsulSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.consul",
		New: func() caddy.Module { return new(ConsulSource) },
	}
}

// Provision sets up the Consul data source.
func (c *ConsulSource) Provision(ctx caddy.Context) error {
	c.logger = ctx.Logger()

	// Set defaults
	if c.Address == "" {
		c.Address = "localhost:8500"
	}
	if c.Scheme == "" {
		c.Scheme = "http"
	}
	if c.Prefix == "" {
		c.Prefix = "caddy/routing/"
	}
	if c.WaitTime == 0 {
		c.WaitTime = caddy.Duration(5 * time.Minute)
	}

	// Initialize LRU cache with negative caching support
	c.cache = cache.NewRouteCache(c.MaxCacheSize)

	// Build Consul client config
	config := &api.Config{
		Address:    c.Address,
		Scheme:     c.Scheme,
		Datacenter: c.Datacenter,
		Token:      c.Token,
		Namespace:  c.Namespace,
	}

	// Configure TLS if enabled
	if c.TLSEnabled {
		config.TLSConfig = api.TLSConfig{
			Address:            c.Address,
			CertFile:           c.TLSCertFile,
			KeyFile:            c.TLSKeyFile,
			CAFile:             c.TLSCAFile,
			// #nosec G402 -- configurable for dev/test environments; default is secure verification.
			InsecureSkipVerify: c.TLSSkipVerify,
		}
	}

	// Create Consul client
	var err error
	c.client, err = api.NewClient(config)
	if err != nil {
		return fmt.Errorf("creating Consul client: %v", err)
	}

	// Initial health check and data load
	loadTimeout := time.Duration(c.InitialLoadTimeout)
	if loadTimeout == 0 {
		loadTimeout = datasource.DefaultInitialLoadTimeout
	}
	loadCtx, cancel := context.WithTimeout(ctx.Context, loadTimeout)
	defer cancel()
	if err := c.initialLoad(loadCtx); err != nil {
		c.logger.Warn("initial Consul load failed, will retry", zap.Error(err))
		c.lastError.Store(err.Error())
		c.healthy.Store(false)
	} else {
		c.healthy.Store(true)
		c.lastError.Store("")
	}

	// Start watcher
	watchCtx, cancel := context.WithCancel(ctx.Context)
	c.cancelWatch = cancel
	c.watchWg.Add(1)
	go c.watchLoop(watchCtx)

	c.logger.Info("Consul data source provisioned",
		zap.String("address", c.Address),
		zap.String("prefix", c.Prefix),
	)

	// Register for Admin API inspection
	c.adminName = datasource.RegisterAdminSource(c)

	return nil
}

// Cleanup releases resources.
func (c *ConsulSource) Cleanup() error {
	datasource.UnregisterAdminSource(c.adminName)
	c.adminName = ""

	if c.cancelWatch != nil {
		c.cancelWatch()
	}
	c.watchWg.Wait()
	return nil
}

// Get retrieves the route configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (c *ConsulSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	fullKey := c.Prefix + key

	// Try cache first
	if cached := c.cache.Get(fullKey); cached != nil {
		return cached, nil
	}

	// Check negative cache
	if c.cache.IsNegativeCached(fullKey) {
		return nil, nil
	}

	// If unhealthy, don't try to fetch from Consul
	if !c.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := c.sfGroup.Do(fullKey, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if cached := c.cache.Get(fullKey); cached != nil {
			return cached, nil
		}

		// Fetch from Consul
		kv := c.client.KV()
		pair, _, err := kv.Get(fullKey, nil)
		if err != nil {
			c.logger.Warn("Consul get failed", zap.String("key", fullKey), zap.Error(err))
			c.lastError.Store(err.Error())
			return nil, err
		}

		if pair == nil {
			// Cache negative result to avoid repeated lookups for missing keys
			c.cache.SetNegative(fullKey)
			return nil, nil
		}

		// Parse and cache the config
		config, err := datasource.ParseRouteConfig(pair.Value)
		if err != nil {
			metrics.RecordRouteConfigParseError(c.AdminType())
			c.logger.Error("failed to parse route config", zap.String("key", fullKey), zap.Error(err))
			c.lastError.Store(err.Error())
			return nil, err
		}

		if config != nil {
			c.cache.Set(fullKey, config)
		}

		return config, nil
	})

	if err != nil {
		return nil, err
	}
	c.lastError.Store("")
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// AdminType returns the short type name for Admin API.
func (*ConsulSource) AdminType() string { return "consul" }

// AdminLastError returns the most recent error message (best-effort).
func (c *ConsulSource) AdminLastError() string {
	if v := c.lastError.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AdminListRoutes returns a snapshot of cached routes.
func (c *ConsulSource) AdminListRoutes() []datasource.AdminRouteInfo {
	if c.cache == nil {
		return nil
	}
	routes := make([]datasource.AdminRouteInfo, 0, c.cache.Len())
	c.cache.Range(func(k string, cfg *datasource.RouteConfig) bool {
		key := strings.TrimPrefix(k, c.Prefix)
		routes = append(routes, datasource.SummarizeRouteConfig(key, cfg))
		return true
	})
	return routes
}

// AdminCacheStats returns a snapshot of internal cache stats.
func (c *ConsulSource) AdminCacheStats() datasource.AdminCacheStats {
	if c.cache == nil {
		return datasource.AdminCacheStats{}
	}
	h, m, nh, hr := c.cache.Stats()
	return datasource.AdminCacheStats{
		Entries:      c.cache.Len(),
		MaxSize:      c.cache.MaxSize(),
		Hits:         h,
		Misses:       m,
		NegativeHits: nh,
		HitRate:      hr,
	}
}

// AdminClearCache clears all internal caches.
func (c *ConsulSource) AdminClearCache() {
	if c.cache != nil {
		c.cache.Clear()
	}
}

// Healthy returns true if the Consul connection is healthy.
func (c *ConsulSource) Healthy() bool {
	return c.healthy.Load()
}

// initialLoad loads all existing routing configurations from Consul.
func (c *ConsulSource) initialLoad(ctx context.Context) error {
	kv := c.client.KV()

	// List all keys with prefix
	q := (&api.QueryOptions{}).WithContext(ctx)
	pairs, meta, err := kv.List(c.Prefix, q)
	if err != nil {
		return fmt.Errorf("initial load failed: %v", err)
	}

	c.lastIndex.Store(meta.LastIndex)

	for _, pair := range pairs {
		config, err := datasource.ParseRouteConfig(pair.Value)
		if err != nil {
			metrics.RecordRouteConfigParseError(c.AdminType())
			c.logger.Warn("failed to parse config during initial load",
				zap.String("key", pair.Key),
				zap.Error(err),
			)
			continue
		}
		if config != nil {
			c.cache.Set(pair.Key, config)
		}
	}

	c.logger.Info("initial Consul load completed", zap.Int("count", len(pairs)))
	return nil
}

// watchLoop continuously watches for changes in Consul.
func (c *ConsulSource) watchLoop(ctx context.Context) {
	defer c.watchWg.Done()

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.watch(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("watch error, will retry", zap.Error(err), zap.Duration("backoff", backoff))
			c.healthy.Store(false)

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

// watch uses Consul's blocking queries to watch for changes.
func (c *ConsulSource) watch(ctx context.Context) error {
	kv := c.client.KV()

	c.healthy.Store(true)
	c.logger.Debug("Consul watch started", zap.String("prefix", c.Prefix))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Use blocking query with last known index
		opts := &api.QueryOptions{
			WaitIndex: c.lastIndex.Load(),
			WaitTime:  time.Duration(c.WaitTime),
		}

		pairs, meta, err := kv.List(c.Prefix, opts)
		if err != nil {
			return fmt.Errorf("watch query failed: %v", err)
		}

		// Check if index changed
		if meta.LastIndex == c.lastIndex.Load() {
			continue
		}
		c.lastIndex.Store(meta.LastIndex)

		// Build new cache state
		newKeys := make(map[string]bool)
		for _, pair := range pairs {
			newKeys[pair.Key] = true
			config, err := datasource.ParseRouteConfig(pair.Value)
			if err != nil {
				metrics.RecordRouteConfigParseError(c.AdminType())
				c.logger.Warn("failed to parse updated config",
					zap.String("key", pair.Key),
					zap.Error(err),
				)
				continue
			}
			if config != nil {
				c.cache.Set(pair.Key, config)
				c.logger.Info("route config updated", zap.String("key", pair.Key))
			}
		}

		// Remove deleted keys - use Keys() method from RouteCache
		for _, k := range c.cache.Keys() {
			if !newKeys[k] {
				c.cache.Delete(k)
				c.logger.Info("route config deleted", zap.String("key", k))
			}
		}
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (c *ConsulSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "address":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Address = d.Val()

			case "addr":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Address = d.Val()

			case "scheme":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Scheme = d.Val()

			case "datacenter":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Datacenter = d.Val()

			case "token":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Token = d.Val()

			case "prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Prefix = d.Val()

			case "namespace":
				if !d.NextArg() {
					return d.ArgErr()
				}
				c.Namespace = d.Val()

			case "wait_time":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid wait_time: %v", err)
				}
				c.WaitTime = caddy.Duration(dur)

			case "initial_load_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid initial_load_timeout: %v", err)
				}
				c.InitialLoadTimeout = caddy.Duration(dur)

			case "tls":
				c.TLSEnabled = true
				for nesting := d.Nesting(); d.NextBlock(nesting); {
					switch d.Val() {
					case "cert":
						if !d.NextArg() {
							return d.ArgErr()
						}
						c.TLSCertFile = d.Val()
					case "key":
						if !d.NextArg() {
							return d.ArgErr()
						}
						c.TLSKeyFile = d.Val()
					case "ca":
						if !d.NextArg() {
							return d.ArgErr()
						}
						c.TLSCAFile = d.Val()
					case "skip_verify":
						c.TLSSkipVerify = true
					default:
						return d.Errf("unknown tls subdirective: %s", d.Val())
					}
				}

			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}

	return nil
}

// Validate implements caddy.Validator.
func (c *ConsulSource) Validate() error {
	if c.Scheme != "" && c.Scheme != "http" && c.Scheme != "https" {
		return fmt.Errorf("scheme must be 'http' or 'https'")
	}
	if c.WaitTime < 0 {
		return fmt.Errorf("wait_time must be non-negative")
	}
	if c.TLSEnabled {
		if (c.TLSCertFile != "" && c.TLSKeyFile == "") || (c.TLSCertFile == "" && c.TLSKeyFile != "") {
			return fmt.Errorf("both tls_cert_file and tls_key_file must be provided together")
		}
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*ConsulSource)(nil)
	_ caddy.Provisioner     = (*ConsulSource)(nil)
	_ caddy.CleanerUpper    = (*ConsulSource)(nil)
	_ caddy.Validator       = (*ConsulSource)(nil)
	_ datasource.DataSource = (*ConsulSource)(nil)
	_ caddyfile.Unmarshaler = (*ConsulSource)(nil)
)
