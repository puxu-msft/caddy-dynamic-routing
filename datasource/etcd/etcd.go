// Package etcd provides an etcd-based data source for dynamic routing configuration.
package etcd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

func init() {
	caddy.RegisterModule(&EtcdSource{})
}

// EtcdSource implements datasource.DataSource using etcd as the backend.
type EtcdSource struct {
	// Endpoints is the list of etcd server endpoints.
	Endpoints []string `json:"endpoints,omitempty"`

	// Prefix is the key prefix for routing configurations in etcd.
	// Default: "/caddy/routing/"
	Prefix string `json:"prefix,omitempty"`

	// Username for etcd authentication.
	Username string `json:"username,omitempty"`

	// Password for etcd authentication.
	Password string `json:"password,omitempty"`

	// DialTimeout is the timeout for establishing connection to etcd.
	// Default: 5s
	DialTimeout caddy.Duration `json:"dial_timeout,omitempty"`

	// RequestTimeout is the timeout for etcd requests.
	// Default: 2s
	RequestTimeout caddy.Duration `json:"request_timeout,omitempty"`

	// MaxCacheSize is the maximum number of entries in the cache.
	// Default: 10000
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// TLS configuration
	TLSEnabled    bool   `json:"tls_enabled,omitempty"`
	TLSCertFile   string `json:"tls_cert_file,omitempty"`
	TLSKeyFile    string `json:"tls_key_file,omitempty"`
	TLSCAFile     string `json:"tls_ca_file,omitempty"`
	TLSSkipVerify bool   `json:"tls_skip_verify,omitempty"`

	// Internal state
	client      *clientv3.Client
	cache       *cache.RouteCache
	healthy     atomic.Bool
	logger      *zap.Logger
	cancelWatch context.CancelFunc
	sfGroup     singleflight.Group // Coalesce concurrent requests for same key
}

// CaddyModule returns the Caddy module information.
func (*EtcdSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.etcd",
		New: func() caddy.Module { return new(EtcdSource) },
	}
}

// Provision sets up the etcd data source.
func (e *EtcdSource) Provision(ctx caddy.Context) error {
	e.logger = ctx.Logger()

	// Set defaults
	if e.Prefix == "" {
		e.Prefix = "/caddy/routing/"
	}
	if e.DialTimeout == 0 {
		e.DialTimeout = caddy.Duration(5 * time.Second)
	}
	if e.RequestTimeout == 0 {
		e.RequestTimeout = caddy.Duration(2 * time.Second)
	}

	// Initialize LRU cache
	e.cache = cache.NewRouteCache(e.MaxCacheSize)

	// Build client config
	config := clientv3.Config{
		Endpoints:   e.Endpoints,
		DialTimeout: time.Duration(e.DialTimeout),
		Username:    e.Username,
		Password:    e.Password,
	}

	// Configure TLS if enabled
	if e.TLSEnabled {
		tlsConfig, err := e.buildTLSConfig()
		if err != nil {
			return fmt.Errorf("building TLS config: %v", err)
		}
		config.TLS = tlsConfig
	}

	// Create etcd client
	var err error
	e.client, err = clientv3.New(config)
	if err != nil {
		return fmt.Errorf("creating etcd client: %v", err)
	}

	// Initial health check and data load
	if err := e.initialLoad(ctx); err != nil {
		e.logger.Warn("initial etcd load failed, will retry", zap.Error(err))
		e.healthy.Store(false)
	} else {
		e.healthy.Store(true)
	}

	// Start watcher
	watchCtx, cancel := context.WithCancel(context.Background())
	e.cancelWatch = cancel
	go e.watchLoop(watchCtx)

	e.logger.Info("etcd data source provisioned",
		zap.Strings("endpoints", e.Endpoints),
		zap.String("prefix", e.Prefix),
		zap.Int("max_cache_size", e.MaxCacheSize),
	)

	return nil
}

// Cleanup releases resources.
func (e *EtcdSource) Cleanup() error {
	if e.cancelWatch != nil {
		e.cancelWatch()
	}

	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

// Get retrieves the route configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (e *EtcdSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	fullKey := e.Prefix + key

	// Try cache first
	if cached := e.cache.Get(fullKey); cached != nil {
		return cached, nil
	}

	// If unhealthy, don't try to fetch from etcd
	if !e.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := e.sfGroup.Do(fullKey, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if cached := e.cache.Get(fullKey); cached != nil {
			return cached, nil
		}

		// Fetch from etcd with timeout
		reqCtx, cancel := context.WithTimeout(ctx, time.Duration(e.RequestTimeout))
		defer cancel()

		resp, err := e.client.Get(reqCtx, fullKey)
		if err != nil {
			e.logger.Warn("etcd get failed", zap.String("key", fullKey), zap.Error(err))
			return nil, err
		}

		if len(resp.Kvs) == 0 {
			// Cache negative result to avoid repeated lookups for missing keys
			e.cache.SetNegative(fullKey)
			return nil, nil
		}

		// Parse and cache the config
		config, err := datasource.ParseRouteConfig(resp.Kvs[0].Value)
		if err != nil {
			e.logger.Error("failed to parse route config", zap.String("key", fullKey), zap.Error(err))
			return nil, err
		}

		if config != nil {
			e.cache.Set(fullKey, config)
		}

		return config, nil
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// Healthy returns true if the etcd connection is healthy.
func (e *EtcdSource) Healthy() bool {
	return e.healthy.Load()
}

// initialLoad loads all existing routing configurations from etcd.
func (e *EtcdSource) initialLoad(ctx caddy.Context) error {
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(e.DialTimeout))
	defer cancel()

	resp, err := e.client.Get(reqCtx, e.Prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("initial load failed: %v", err)
	}

	for _, kv := range resp.Kvs {
		config, err := datasource.ParseRouteConfig(kv.Value)
		if err != nil {
			e.logger.Warn("failed to parse config during initial load",
				zap.String("key", string(kv.Key)),
				zap.Error(err),
			)
			continue
		}
		if config != nil {
			e.cache.Set(string(kv.Key), config)
		}
	}

	e.logger.Info("initial etcd load completed", zap.Int("count", len(resp.Kvs)))
	return nil
}

// watchLoop continuously watches for changes in etcd.
func (e *EtcdSource) watchLoop(ctx context.Context) {
	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := e.watch(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Warn("watch error, will retry", zap.Error(err), zap.Duration("backoff", backoff))
			e.healthy.Store(false)

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
			// Reset backoff on successful watch
			backoff = time.Second
		}
	}
}

// watch watches for changes in etcd and updates the cache.
func (e *EtcdSource) watch(ctx context.Context) error {
	rch := e.client.Watch(ctx, e.Prefix, clientv3.WithPrefix(), clientv3.WithPrevKV())

	e.healthy.Store(true)
	e.logger.Debug("etcd watch started", zap.String("prefix", e.Prefix))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case wresp, ok := <-rch:
			if !ok {
				return fmt.Errorf("watch channel closed")
			}

			if wresp.Err() != nil {
				return wresp.Err()
			}

			for _, ev := range wresp.Events {
				key := string(ev.Kv.Key)

				switch ev.Type {
				case clientv3.EventTypePut:
					config, err := datasource.ParseRouteConfig(ev.Kv.Value)
					if err != nil {
						e.logger.Warn("failed to parse updated config",
							zap.String("key", key),
							zap.Error(err),
						)
						continue
					}
					if config != nil {
						e.cache.Set(key, config)
						e.logger.Info("route config updated", zap.String("key", key))
					}

				case clientv3.EventTypeDelete:
					e.cache.Delete(key)
					e.logger.Info("route config deleted", zap.String("key", key))
				}
			}
		}
	}
}

// buildTLSConfig builds the TLS configuration for etcd.
func (e *EtcdSource) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: e.TLSSkipVerify,
	}

	if e.TLSCertFile != "" && e.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(e.TLSCertFile, e.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %v", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if e.TLSCAFile != "" {
		caCert, err := os.ReadFile(e.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %v", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (e *EtcdSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "endpoints":
				e.Endpoints = d.RemainingArgs()
				if len(e.Endpoints) == 0 {
					return d.ArgErr()
				}

			case "prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				e.Prefix = d.Val()

			case "username":
				if !d.NextArg() {
					return d.ArgErr()
				}
				e.Username = d.Val()

			case "password":
				if !d.NextArg() {
					return d.ArgErr()
				}
				e.Password = d.Val()

			case "dial_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid dial_timeout: %v", err)
				}
				e.DialTimeout = caddy.Duration(dur)

			case "request_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid request_timeout: %v", err)
				}
				e.RequestTimeout = caddy.Duration(dur)

			case "tls":
				e.TLSEnabled = true
				for nesting := d.Nesting(); d.NextBlock(nesting); {
					switch d.Val() {
					case "cert":
						if !d.NextArg() {
							return d.ArgErr()
						}
						e.TLSCertFile = d.Val()
					case "key":
						if !d.NextArg() {
							return d.ArgErr()
						}
						e.TLSKeyFile = d.Val()
					case "ca":
						if !d.NextArg() {
							return d.ArgErr()
						}
						e.TLSCAFile = d.Val()
					case "skip_verify":
						e.TLSSkipVerify = true
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
func (e *EtcdSource) Validate() error {
	if len(e.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint is required")
	}
	if e.DialTimeout < 0 {
		return fmt.Errorf("dial_timeout must be non-negative")
	}
	if e.RequestTimeout < 0 {
		return fmt.Errorf("request_timeout must be non-negative")
	}
	if e.MaxCacheSize < 0 {
		return fmt.Errorf("max_cache_size must be non-negative")
	}
	if e.TLSEnabled {
		if (e.TLSCertFile != "" && e.TLSKeyFile == "") || (e.TLSCertFile == "" && e.TLSKeyFile != "") {
			return fmt.Errorf("both tls_cert_file and tls_key_file must be provided together")
		}
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*EtcdSource)(nil)
	_ caddy.Provisioner     = (*EtcdSource)(nil)
	_ caddy.CleanerUpper    = (*EtcdSource)(nil)
	_ caddy.Validator       = (*EtcdSource)(nil)
	_ datasource.DataSource = (*EtcdSource)(nil)
	_ caddyfile.Unmarshaler = (*EtcdSource)(nil)
)
