// Package caddyslb provides dynamic upstreams support for Caddy's reverse proxy.
package caddyslb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/extractor"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(&DynamicUpstreams{})
}

// DynamicUpstreams is an UpstreamSource that retrieves upstream lists
// dynamically from external data sources like etcd, Redis, or Consul.
//
// Unlike the DynamicSelection policy which selects from a static pool,
// DynamicUpstreams actually provides the pool itself based on dynamic configuration.
type DynamicUpstreams struct {
	// Key is the placeholder expression used to extract the routing key from requests.
	// If empty, a default key will be used to fetch a global upstream list.
	Key string `json:"key,omitempty"`

	// DefaultKey is used when no key is extracted from the request.
	DefaultKey string `json:"default_key,omitempty"`

	// DataSourceRaw is the raw JSON configuration for the data source.
	DataSourceRaw json.RawMessage `json:"data_source,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies.dynamic.sources inline_key=source"`

	// RefreshInterval is how often to refresh the upstream list from the data source.
	// If zero, upstreams are fetched on every request (not recommended for performance).
	// Default is 5 seconds.
	RefreshInterval caddy.Duration `json:"refresh_interval,omitempty"`

	// Internal fields
	keyExtractor extractor.KeyExtractor
	dataSource   datasource.DataSource
	logger       *zap.Logger

	// Cached upstreams per key
	upstreamCache sync.Map // map[string]*cachedUpstreams

	// Singleflight to prevent thundering herd on cache refresh
	sfGroup singleflight.Group
}

type cachedUpstreams struct {
	upstreams  []*reverseproxy.Upstream
	lastUpdate time.Time
}

// CaddyModule returns the Caddy module information.
func (*DynamicUpstreams) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.upstreams.dynamic_source",
		New: func() caddy.Module { return new(DynamicUpstreams) },
	}
}

// Provision sets up the dynamic upstreams.
func (d *DynamicUpstreams) Provision(ctx caddy.Context) error {
	d.logger = ctx.Logger()

	// Set default refresh interval
	if d.RefreshInterval == 0 {
		d.RefreshInterval = caddy.Duration(5 * time.Second)
	}

	// Parse key expression
	if d.Key != "" {
		var err error
		d.keyExtractor, err = extractor.NewFromExpression(d.Key)
		if err != nil {
			return fmt.Errorf("parsing key expression: %v", err)
		}
	}

	// Load data source module
	if d.DataSourceRaw != nil {
		val, err := ctx.LoadModule(d, "DataSourceRaw")
		if err != nil {
			return fmt.Errorf("loading data source: %v", err)
		}
		d.dataSource = val.(datasource.DataSource)
	}

	if d.dataSource == nil {
		return fmt.Errorf("data source is required")
	}

	d.logger.Info("dynamic upstreams provisioned",
		zap.String("key", d.Key),
		zap.String("default_key", d.DefaultKey),
		zap.Duration("refresh_interval", time.Duration(d.RefreshInterval)),
	)

	return nil
}

// Cleanup releases resources.
func (d *DynamicUpstreams) Cleanup() error {
	return nil
}

// GetUpstreams implements reverseproxy.UpstreamSource.
// This method must be very fast, so it uses caching with periodic refresh.
// Uses singleflight to prevent thundering herd when cache expires.
func (d *DynamicUpstreams) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	// Determine the key to use
	key := d.getKey(r)
	if key == "" {
		key = d.DefaultKey
	}
	if key == "" {
		key = "_default_"
	}

	// Check cache
	if cached, ok := d.upstreamCache.Load(key); ok {
		c := cached.(*cachedUpstreams)
		if time.Since(c.lastUpdate) < time.Duration(d.RefreshInterval) {
			metrics.RecordCacheHit(d.dataSourceType())
			return c.upstreams, nil
		}
	}

	metrics.RecordCacheMiss(d.dataSourceType())

	// Use singleflight to prevent thundering herd on cache refresh
	result, err, _ := d.sfGroup.Do(key, func() (interface{}, error) {
		// Double-check cache (another goroutine may have refreshed it)
		if cached, ok := d.upstreamCache.Load(key); ok {
			c := cached.(*cachedUpstreams)
			if time.Since(c.lastUpdate) < time.Duration(d.RefreshInterval) {
				return c.upstreams, nil
			}
		}

		// Fetch from data source
		timer := metrics.NewTimer()
		config, err := d.dataSource.Get(r.Context(), key)
		timer.ObserveDuration(metrics.DataSourceLatency.WithLabelValues(d.dataSourceType()))

		if err != nil {
			d.logger.Warn("failed to get upstreams from data source",
				zap.String("key", key),
				zap.Error(err),
			)
			// Return cached upstreams if available (stale data is better than no data)
			if cached, ok := d.upstreamCache.Load(key); ok {
				return cached.(*cachedUpstreams).upstreams, nil
			}
			return nil, err
		}

		if config == nil {
			return ([]*reverseproxy.Upstream)(nil), nil
		}

		// Convert route config to upstreams
		upstreams := d.configToUpstreams(config)

		// Cache the result
		d.upstreamCache.Store(key, &cachedUpstreams{
			upstreams:  upstreams,
			lastUpdate: time.Now(),
		})

		return upstreams, nil
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.([]*reverseproxy.Upstream), nil
}

// getKey extracts the routing key from the request.
func (d *DynamicUpstreams) getKey(r *http.Request) string {
	if d.keyExtractor == nil {
		return ""
	}

	replVal := r.Context().Value(caddy.ReplacerCtxKey)
	if replVal == nil {
		return ""
	}
	repl, ok := replVal.(*caddy.Replacer)
	if !ok || repl == nil {
		return ""
	}

	return d.keyExtractor.Extract(r, repl)
}

// configToUpstreams converts a RouteConfig to a list of Upstreams.
func (d *DynamicUpstreams) configToUpstreams(config *datasource.RouteConfig) []*reverseproxy.Upstream {
	var upstreams []*reverseproxy.Upstream

	// Simple upstream
	if config.Upstream != "" {
		upstreams = append(upstreams, &reverseproxy.Upstream{
			Dial: config.Upstream,
		})
	}

	// Collect upstreams from all rules
	for _, rule := range config.Rules {
		for _, u := range rule.Upstreams {
			upstreams = append(upstreams, &reverseproxy.Upstream{
				Dial: u.Address,
			})
		}
	}

	// Deduplicate
	seen := make(map[string]bool)
	result := make([]*reverseproxy.Upstream, 0, len(upstreams))
	for _, u := range upstreams {
		if !seen[u.Dial] {
			seen[u.Dial] = true
			result = append(result, u)
		}
	}

	return result
}

// dataSourceType returns the data source type name for metrics.
func (d *DynamicUpstreams) dataSourceType() string {
	if d.dataSource == nil {
		return "unknown"
	}
	info := d.dataSource.CaddyModule()
	id := string(info.ID)
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (d *DynamicUpstreams) UnmarshalCaddyfile(disp *caddyfile.Dispenser) error {
	for disp.Next() {
		for disp.NextBlock(0) {
			switch disp.Val() {
			case "key":
				if !disp.NextArg() {
					return disp.ArgErr()
				}
				d.Key = disp.Val()

			case "default_key":
				if !disp.NextArg() {
					return disp.ArgErr()
				}
				d.DefaultKey = disp.Val()

			case "refresh_interval":
				if !disp.NextArg() {
					return disp.ArgErr()
				}
				dur, err := caddy.ParseDuration(disp.Val())
				if err != nil {
					return disp.Errf("invalid duration: %v", err)
				}
				d.RefreshInterval = caddy.Duration(dur)

			case "etcd", "redis", "consul", "file", "http":
				sourceType := disp.Val()
				sourceJSON := map[string]interface{}{
					"source": sourceType,
				}

				for nesting := disp.Nesting(); disp.NextBlock(nesting); {
					key := disp.Val()
					if !disp.NextArg() {
						return disp.ArgErr()
					}
					value := disp.Val()

					// Handle array values
					if key == "endpoints" || key == "addresses" {
						values := []string{value}
						for disp.NextArg() {
							values = append(values, disp.Val())
						}
						sourceJSON[key] = values
					} else {
						sourceJSON[key] = value
					}
				}

				raw, err := json.Marshal(sourceJSON)
				if err != nil {
					return disp.Errf("encoding data source config: %v", err)
				}
				d.DataSourceRaw = raw

			default:
				return disp.Errf("unknown subdirective: %s", disp.Val())
			}
		}
	}

	return nil
}

// Validate implements caddy.Validator.
func (d *DynamicUpstreams) Validate() error {
	if d.RefreshInterval < 0 {
		return fmt.Errorf("refresh_interval must be non-negative")
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module                = (*DynamicUpstreams)(nil)
	_ caddy.Provisioner           = (*DynamicUpstreams)(nil)
	_ caddy.CleanerUpper          = (*DynamicUpstreams)(nil)
	_ caddy.Validator             = (*DynamicUpstreams)(nil)
	_ reverseproxy.UpstreamSource = (*DynamicUpstreams)(nil)
	_ caddyfile.Unmarshaler       = (*DynamicUpstreams)(nil)
)
