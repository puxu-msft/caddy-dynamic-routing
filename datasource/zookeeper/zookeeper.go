// Package zookeeper provides a Zookeeper data source for dynamic routing configuration.
package zookeeper

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/go-zookeeper/zk"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

func init() {
	caddy.RegisterModule(&ZookeeperSource{})
}

// ZookeeperSource implements datasource.DataSource using Zookeeper.
type ZookeeperSource struct {
	// Servers is the list of Zookeeper servers to connect to.
	Servers []string `json:"servers,omitempty"`

	// BasePath is the root path for routing configurations.
	// Default is "/caddy/routing".
	BasePath string `json:"base_path,omitempty"`

	// SessionTimeout is the Zookeeper session timeout.
	// Default is 10s.
	SessionTimeout caddy.Duration `json:"session_timeout,omitempty"`

	// MaxCacheSize is the maximum number of entries in the local cache.
	// Default is 10000.
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// Username for SASL authentication.
	Username string `json:"username,omitempty"`

	// Password for SASL authentication.
	Password string `json:"password,omitempty"`

	// internal fields
	conn      *zk.Conn
	cache     *cache.RouteCache
	healthy   atomic.Bool
	logger    *zap.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	watcherWg sync.WaitGroup
	sfGroup   singleflight.Group // Coalesce concurrent requests for same key
}

// CaddyModule returns the Caddy module information.
func (*ZookeeperSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.zookeeper",
		New: func() caddy.Module { return new(ZookeeperSource) },
	}
}

// Provision sets up the Zookeeper data source.
func (z *ZookeeperSource) Provision(ctx caddy.Context) error {
	z.logger = ctx.Logger()

	// Set defaults
	if z.BasePath == "" {
		z.BasePath = "/caddy/routing"
	}
	if z.SessionTimeout == 0 {
		z.SessionTimeout = caddy.Duration(10 * time.Second)
	}
	if z.MaxCacheSize <= 0 {
		z.MaxCacheSize = 10000
	}

	// Initialize cache
	z.cache = cache.NewRouteCache(z.MaxCacheSize)

	// Create context for watchers
	z.ctx, z.cancel = context.WithCancel(context.Background())

	// Connect to Zookeeper
	conn, eventChan, err := zk.Connect(z.Servers, time.Duration(z.SessionTimeout))
	if err != nil {
		return fmt.Errorf("failed to connect to zookeeper: %w", err)
	}
	z.conn = conn

	// Add authentication if configured
	if z.Username != "" && z.Password != "" {
		auth := fmt.Sprintf("%s:%s", z.Username, z.Password)
		if err := z.conn.AddAuth("digest", []byte(auth)); err != nil {
			z.conn.Close()
			return fmt.Errorf("zookeeper authentication failed: %w", err)
		}
	}

	// Start session event handler
	go z.handleSessionEvents(eventChan)

	// Initial load
	if err := z.initialLoad(); err != nil {
		z.logger.Warn("initial zookeeper load failed", zap.Error(err))
		z.healthy.Store(false)
	} else {
		z.healthy.Store(true)
	}

	// Start watchers
	z.watcherWg.Add(1)
	go z.watchChildren()

	z.logger.Info("zookeeper data source initialized",
		zap.Strings("servers", z.Servers),
		zap.String("base_path", z.BasePath))

	return nil
}

// Cleanup releases Zookeeper resources.
func (z *ZookeeperSource) Cleanup() error {
	if z.cancel != nil {
		z.cancel()
	}
	z.watcherWg.Wait()
	if z.conn != nil {
		z.conn.Close()
	}
	z.logger.Info("zookeeper data source cleaned up")
	return nil
}

// Get retrieves routing configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (z *ZookeeperSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	// Check cache first
	if config := z.cache.Get(key); config != nil {
		return config, nil
	}

	// Check negative cache
	if z.cache.IsNegativeCached(key) {
		return nil, nil
	}

	// If unhealthy, don't try to fetch from Zookeeper
	if !z.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := z.sfGroup.Do(key, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if config := z.cache.Get(key); config != nil {
			return config, nil
		}

		// Fetch from Zookeeper
		nodePath := path.Join(z.BasePath, key)
		data, _, err := z.conn.Get(nodePath)
		if err != nil {
			if err == zk.ErrNoNode {
				// Cache negative result to avoid repeated lookups for missing keys
				z.cache.SetNegative(key)
				return nil, nil
			}
			return nil, fmt.Errorf("failed to get znode %s: %w", nodePath, err)
		}

		config, err := datasource.ParseRouteConfig(data)
		if err != nil {
			z.logger.Warn("failed to parse route config",
				zap.String("key", key),
				zap.Error(err))
			return nil, nil
		}

		// Cache the result
		z.cache.Set(key, config)
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

// Healthy returns the health status of the Zookeeper connection.
func (z *ZookeeperSource) Healthy() bool {
	return z.healthy.Load()
}

// initialLoad loads all existing configurations from Zookeeper.
func (z *ZookeeperSource) initialLoad() error {
	// Ensure base path exists
	exists, _, err := z.conn.Exists(z.BasePath)
	if err != nil {
		return fmt.Errorf("failed to check base path: %w", err)
	}
	if !exists {
		z.logger.Info("base path does not exist, will be created when first config is added",
			zap.String("path", z.BasePath))
		return nil
	}

	// Get all children
	children, _, err := z.conn.Children(z.BasePath)
	if err != nil {
		return fmt.Errorf("failed to get children: %w", err)
	}

	for _, child := range children {
		nodePath := path.Join(z.BasePath, child)
		data, _, err := z.conn.Get(nodePath)
		if err != nil {
			z.logger.Warn("failed to get child node", zap.String("path", nodePath), zap.Error(err))
			continue
		}

		config, err := datasource.ParseRouteConfig(data)
		if err != nil {
			z.logger.Warn("failed to parse config", zap.String("key", child), zap.Error(err))
			continue
		}

		z.cache.Set(child, config)
	}

	z.logger.Info("loaded initial configurations", zap.Int("count", len(children)))
	return nil
}

// handleSessionEvents handles Zookeeper session events.
func (z *ZookeeperSource) handleSessionEvents(eventChan <-chan zk.Event) {
	for {
		select {
		case <-z.ctx.Done():
			return
		case event, ok := <-eventChan:
			if !ok {
				return
			}
			switch event.State {
			case zk.StateConnected, zk.StateHasSession:
				z.healthy.Store(true)
				z.logger.Info("zookeeper connected", zap.String("state", event.State.String()))
			case zk.StateDisconnected, zk.StateExpired:
				z.healthy.Store(false)
				z.logger.Warn("zookeeper disconnected", zap.String("state", event.State.String()))
			}
		}
	}
}

// watchChildren watches for changes in the base path.
func (z *ZookeeperSource) watchChildren() {
	defer z.watcherWg.Done()

	for {
		select {
		case <-z.ctx.Done():
			return
		default:
		}

		// Set up watch on children
		children, _, eventChan, err := z.conn.ChildrenW(z.BasePath)
		if err != nil {
			if err == zk.ErrNoNode {
				// Base path doesn't exist, wait and retry
				time.Sleep(5 * time.Second)
				continue
			}
			z.logger.Error("failed to watch children", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}

		// Set up watches on each child
		for _, child := range children {
			go z.watchNode(child)
		}

		// Wait for event
		select {
		case <-z.ctx.Done():
			return
		case event := <-eventChan:
			z.logger.Debug("children watch triggered", zap.String("type", event.Type.String()))
			// Children changed, loop will re-watch
		}
	}
}

// watchNode watches a specific node for changes.
func (z *ZookeeperSource) watchNode(key string) {
	nodePath := path.Join(z.BasePath, key)

	for {
		select {
		case <-z.ctx.Done():
			return
		default:
		}

		data, _, eventChan, err := z.conn.GetW(nodePath)
		if err != nil {
			if err == zk.ErrNoNode {
				// Node deleted
				z.cache.Delete(key)
				return
			}
			z.logger.Warn("failed to watch node", zap.String("path", nodePath), zap.Error(err))
			return
		}

		// Update cache
		config, err := datasource.ParseRouteConfig(data)
		if err != nil {
			z.logger.Warn("failed to parse config", zap.String("key", key), zap.Error(err))
		} else {
			z.cache.Set(key, config)
		}

		// Wait for event
		select {
		case <-z.ctx.Done():
			return
		case event := <-eventChan:
			switch event.Type {
			case zk.EventNodeDeleted:
				z.cache.Delete(key)
				z.logger.Debug("node deleted", zap.String("key", key))
				return
			case zk.EventNodeDataChanged:
				z.logger.Debug("node data changed", zap.String("key", key))
				// Loop will re-fetch and re-watch
			}
		}
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (z *ZookeeperSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "servers":
				z.Servers = d.RemainingArgs()
				if len(z.Servers) == 0 {
					return d.ArgErr()
				}
			case "base_path":
				if !d.NextArg() {
					return d.ArgErr()
				}
				z.BasePath = d.Val()
			case "session_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid session_timeout: %v", err)
				}
				z.SessionTimeout = caddy.Duration(dur)
			case "max_cache_size":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var size int
				if _, err := fmt.Sscanf(d.Val(), "%d", &size); err != nil {
					return d.Errf("invalid max_cache_size: %v", err)
				}
				z.MaxCacheSize = size
			case "username":
				if !d.NextArg() {
					return d.ArgErr()
				}
				z.Username = d.Val()
			case "password":
				if !d.NextArg() {
					return d.ArgErr()
				}
				z.Password = d.Val()
			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// Validate implements caddy.Validator.
func (z *ZookeeperSource) Validate() error {
	if len(z.Servers) == 0 {
		return fmt.Errorf("at least one server is required")
	}
	for _, server := range z.Servers {
		if !strings.Contains(server, ":") {
			return fmt.Errorf("server address must include port: %s", server)
		}
	}
	if z.SessionTimeout < 0 {
		return fmt.Errorf("session_timeout must be non-negative")
	}
	if z.MaxCacheSize < 0 {
		return fmt.Errorf("max_cache_size must be non-negative")
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*ZookeeperSource)(nil)
	_ caddy.Provisioner     = (*ZookeeperSource)(nil)
	_ caddy.CleanerUpper    = (*ZookeeperSource)(nil)
	_ caddy.Validator       = (*ZookeeperSource)(nil)
	_ datasource.DataSource = (*ZookeeperSource)(nil)
	_ caddyfile.Unmarshaler = (*ZookeeperSource)(nil)
)
