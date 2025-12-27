// Package http provides an HTTP-based data source for dynamic routing configuration.
package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(&HTTPSource{})
}

// HTTPSource implements datasource.DataSource using HTTP API as the backend.
// It fetches routing configuration from a remote HTTP endpoint.
type HTTPSource struct {
	// URL is the base URL for the routing API.
	// The key will be appended to this URL: {URL}/{key}
	URL string `json:"url,omitempty"`

	// Headers are custom HTTP headers to include in requests.
	Headers map[string]string `json:"headers,omitempty"`

	// Method is the HTTP method to use.
	// Default: "GET"
	Method string `json:"method,omitempty"`

	// Timeout is the request timeout.
	// Default: 5s
	Timeout caddy.Duration `json:"timeout,omitempty"`

	// CacheTTL is the cache time-to-live for responses.
	// Default: 1m
	CacheTTL caddy.Duration `json:"cache_ttl,omitempty"`

	// RefreshInterval is the interval for background cache refresh.
	// If set, all cached keys are refreshed at this interval.
	// Default: 0 (disabled)
	RefreshInterval caddy.Duration `json:"refresh_interval,omitempty"`

	// RetryCount is the number of retry attempts for failed requests.
	// Default: 2
	RetryCount int `json:"retry_count,omitempty"`

	// RetryDelay is the delay between retry attempts.
	// Default: 100ms
	RetryDelay caddy.Duration `json:"retry_delay,omitempty"`

	// TLS configuration
	TLSSkipVerify bool `json:"tls_skip_verify,omitempty"`

	// HealthEndpoint is the URL to check for health.
	// Default: same as URL
	HealthEndpoint string `json:"health_endpoint,omitempty"`

	// MaxCacheSize is the maximum number of entries in the cache.
	// Default: 10000
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// Internal state
	client        *http.Client
	cache         *cache.RouteCache
	healthy       atomic.Bool
	logger        *zap.Logger
	cancelRefresh context.CancelFunc
	refreshWg     sync.WaitGroup
	sfGroup       singleflight.Group // Coalesce concurrent requests for same key

	// Admin / diagnostics
	adminName string
	lastError atomic.Value // string
}

// CaddyModule returns the Caddy module information.
func (*HTTPSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.http",
		New: func() caddy.Module { return new(HTTPSource) },
	}
}

// Provision sets up the HTTP data source.
func (h *HTTPSource) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()

	// Set defaults
	if h.Method == "" {
		h.Method = "GET"
	}
	if h.Timeout == 0 {
		h.Timeout = caddy.Duration(5 * time.Second)
	}
	if h.CacheTTL == 0 {
		h.CacheTTL = caddy.Duration(1 * time.Minute)
	}
	if h.HealthEndpoint == "" {
		h.HealthEndpoint = h.URL
	}
	if h.RetryCount == 0 {
		h.RetryCount = 2
	}
	if h.RetryDelay == 0 {
		h.RetryDelay = caddy.Duration(100 * time.Millisecond)
	}
	if h.MaxCacheSize <= 0 {
		h.MaxCacheSize = 10000
	}

	if h.URL == "" {
		return fmt.Errorf("url is required")
	}

	// Initialize LRU cache with negative caching support
	h.cache = cache.NewRouteCache(h.MaxCacheSize)

	// Create HTTP client
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: h.TLSSkipVerify,
		},
		MaxIdleConns:       100,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: false,
	}
	h.client = &http.Client{
		Transport: transport,
		Timeout:   time.Duration(h.Timeout),
	}

	// Initial health check
	if err := h.healthCheck(); err != nil {
		h.logger.Warn("initial health check failed", zap.Error(err))
		h.lastError.Store(err.Error())
		h.healthy.Store(false)
	} else {
		h.healthy.Store(true)
		h.lastError.Store("")
	}

	// Start background refresh if configured
	if h.RefreshInterval > 0 {
		refreshCtx, cancel := context.WithCancel(context.Background())
		h.cancelRefresh = cancel
		h.refreshWg.Add(1)
		go h.refreshLoop(refreshCtx)
	}

	h.logger.Info("HTTP data source provisioned",
		zap.String("url", h.URL),
		zap.Duration("timeout", time.Duration(h.Timeout)),
		zap.Duration("cache_ttl", time.Duration(h.CacheTTL)),
	)

	// Register for Admin API inspection
	h.adminName = datasource.RegisterAdminSource(h)

	return nil
}

// Cleanup releases resources.
func (h *HTTPSource) Cleanup() error {
	datasource.UnregisterAdminSource(h.adminName)
	h.adminName = ""

	if h.cancelRefresh != nil {
		h.cancelRefresh()
	}
	h.refreshWg.Wait()

	if h.client != nil {
		h.client.CloseIdleConnections()
	}
	return nil
}

// Get retrieves the route configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (h *HTTPSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	// Check cache first
	if cached := h.cache.Get(key); cached != nil {
		return cached, nil
	}

	// Check negative cache
	if h.cache.IsNegativeCached(key) {
		return nil, nil
	}

	// If unhealthy, return nil
	if !h.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := h.sfGroup.Do(key, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if cached := h.cache.Get(key); cached != nil {
			return cached, nil
		}

		// Fetch from HTTP
		config, err := h.fetch(ctx, key)
		if err != nil {
			h.logger.Warn("HTTP fetch failed", zap.String("key", key), zap.Error(err))
			h.lastError.Store(err.Error())
			return nil, err
		}

		if config == nil {
			// Cache negative result to avoid repeated lookups for missing keys
			h.cache.SetNegative(key)
			return nil, nil
		}

		// Cache the result
		h.cache.Set(key, config)
		return config, nil
	})

	if err != nil {
		return nil, err
	}
	h.lastError.Store("")
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// AdminType returns the short type name for Admin API.
func (*HTTPSource) AdminType() string { return "http" }

// AdminLastError returns the most recent error message (best-effort).
func (h *HTTPSource) AdminLastError() string {
	if v := h.lastError.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AdminListRoutes returns a snapshot of cached routes.
func (h *HTTPSource) AdminListRoutes() []datasource.AdminRouteInfo {
	if h.cache == nil {
		return nil
	}
	routes := make([]datasource.AdminRouteInfo, 0, h.cache.Len())
	h.cache.Range(func(k string, cfg *datasource.RouteConfig) bool {
		routes = append(routes, datasource.SummarizeRouteConfig(k, cfg))
		return true
	})
	return routes
}

// AdminCacheStats returns a snapshot of internal cache stats.
func (h *HTTPSource) AdminCacheStats() datasource.AdminCacheStats {
	if h.cache == nil {
		return datasource.AdminCacheStats{}
	}
	hits, misses, negativeHits, hitRate := h.cache.Stats()
	return datasource.AdminCacheStats{
		Entries:      h.cache.Len(),
		MaxSize:      h.cache.MaxSize(),
		Hits:         hits,
		Misses:       misses,
		NegativeHits: negativeHits,
		HitRate:      hitRate,
	}
}

// AdminClearCache clears all internal caches.
func (h *HTTPSource) AdminClearCache() {
	if h.cache != nil {
		h.cache.Clear()
	}
}

// Healthy returns true if the HTTP endpoint is healthy.
func (h *HTTPSource) Healthy() bool {
	return h.healthy.Load()
}

// fetch retrieves configuration from the HTTP endpoint with retry support.
func (h *HTTPSource) fetch(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	url := fmt.Sprintf("%s/%s", h.URL, key)

	var lastErr error
	for attempt := 0; attempt <= h.RetryCount; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(h.RetryDelay)):
			}
			h.logger.Debug("retrying HTTP request",
				zap.String("url", url),
				zap.Int("attempt", attempt),
			)
		}

		config, err := h.doFetch(ctx, url)
		if err == nil {
			return config, nil
		}

		// Don't retry on 404
		if err == errNotFound {
			return nil, nil
		}

		lastErr = err
	}

	return nil, fmt.Errorf("all %d attempts failed: %v", h.RetryCount+1, lastErr)
}

// errNotFound is a sentinel error for 404 responses
var errNotFound = fmt.Errorf("not found")

// doFetch performs a single HTTP fetch request.
func (h *HTTPSource) doFetch(ctx context.Context, url string) (*datasource.RouteConfig, error) {
	req, err := http.NewRequestWithContext(ctx, h.Method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %v", err)
	}

	// Add custom headers
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			h.logger.Debug("failed to close response body", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}

	if resp.StatusCode >= 500 {
		// Server error - retryable
		return nil, fmt.Errorf("server error: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %v", err)
	}

	config, err := datasource.ParseRouteConfig(body)
	if err != nil {
		metrics.RecordRouteConfigParseError(h.AdminType())
		return nil, fmt.Errorf("parsing response: %v", err)
	}

	return config, nil
}

// healthCheck performs a health check against the endpoint.
func (h *HTTPSource) healthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.Timeout))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "HEAD", h.HealthEndpoint, nil)
	if err != nil {
		return err
	}

	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	if err := resp.Body.Close(); err != nil {
		h.logger.Debug("failed to close health check response body", zap.Error(err))
	}

	if resp.StatusCode >= 500 {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	return nil
}

// refreshLoop periodically refreshes cached entries.
func (h *HTTPSource) refreshLoop(ctx context.Context) {
	defer h.refreshWg.Done()

	ticker := time.NewTicker(time.Duration(h.RefreshInterval))
	defer ticker.Stop()

	h.logger.Debug("background refresh started", zap.Duration("interval", time.Duration(h.RefreshInterval)))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.refreshCache(ctx)
			h.doHealthCheck()
		}
	}
}

// refreshCache refreshes all cached entries.
func (h *HTTPSource) refreshCache(ctx context.Context) {
	keys := h.cache.Keys()

	for _, key := range keys {
		config, err := h.fetch(ctx, key)
		if err != nil {
			h.logger.Debug("refresh failed", zap.String("key", key), zap.Error(err))
			continue
		}

		if config != nil {
			h.cache.Set(key, config)
		} else {
			h.cache.Delete(key)
		}
	}
}

// doHealthCheck updates the healthy status.
func (h *HTTPSource) doHealthCheck() {
	if err := h.healthCheck(); err != nil {
		if h.healthy.Load() {
			h.logger.Warn("health check failed", zap.Error(err))
		}
		h.healthy.Store(false)
	} else {
		if !h.healthy.Load() {
			h.logger.Info("health check recovered")
		}
		h.healthy.Store(true)
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (h *HTTPSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.URL = d.Val()

			case "method":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.Method = d.Val()

			case "timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid timeout: %v", err)
				}
				h.Timeout = caddy.Duration(dur)

			case "cache_ttl":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid cache_ttl: %v", err)
				}
				h.CacheTTL = caddy.Duration(dur)

			case "refresh_interval":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid refresh_interval: %v", err)
				}
				h.RefreshInterval = caddy.Duration(dur)

			case "header":
				args := d.RemainingArgs()
				if len(args) != 2 {
					return d.ArgErr()
				}
				if h.Headers == nil {
					h.Headers = make(map[string]string)
				}
				h.Headers[args[0]] = args[1]

			case "tls_skip_verify":
				h.TLSSkipVerify = true

			case "health_endpoint":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.HealthEndpoint = d.Val()

			case "retry_count":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var count int
				if _, err := fmt.Sscanf(d.Val(), "%d", &count); err != nil {
					return d.Errf("invalid retry_count: %v", err)
				}
				h.RetryCount = count

			case "retry_delay":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid retry_delay: %v", err)
				}
				h.RetryDelay = caddy.Duration(dur)

			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}

	return nil
}

// UnmarshalJSON implements json.Unmarshaler to handle Headers as both object and array.
func (h *HTTPSource) UnmarshalJSON(data []byte) error {
	type Alias HTTPSource
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	return json.Unmarshal(data, aux)
}

// Validate implements caddy.Validator.
func (h *HTTPSource) Validate() error {
	if h.URL == "" {
		return fmt.Errorf("url is required")
	}
	if h.Method != "" && h.Method != "GET" && h.Method != "POST" {
		return fmt.Errorf("method must be 'GET' or 'POST'")
	}
	if h.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	if h.CacheTTL < 0 {
		return fmt.Errorf("cache_ttl must be non-negative")
	}
	if h.RefreshInterval < 0 {
		return fmt.Errorf("refresh_interval must be non-negative")
	}
	if h.RetryCount < 0 {
		return fmt.Errorf("retry_count must be non-negative")
	}
	if h.RetryDelay < 0 {
		return fmt.Errorf("retry_delay must be non-negative")
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*HTTPSource)(nil)
	_ caddy.Provisioner     = (*HTTPSource)(nil)
	_ caddy.CleanerUpper    = (*HTTPSource)(nil)
	_ caddy.Validator       = (*HTTPSource)(nil)
	_ datasource.DataSource = (*HTTPSource)(nil)
	_ caddyfile.Unmarshaler = (*HTTPSource)(nil)
)
