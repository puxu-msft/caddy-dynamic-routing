// Package composite provides a composite data source that combines multiple data sources.
package composite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

func init() {
	caddy.RegisterModule(&CompositeSource{})
}

// CombineMode determines how multiple data sources are combined.
type CombineMode string

const (
	// ModeFailover uses the first healthy data source that returns a result.
	// Sources are tried in order; the first successful result is returned.
	ModeFailover CombineMode = "failover"

	// ModeMerge merges results from all data sources.
	// Later sources can override fields from earlier sources.
	ModeMerge CombineMode = "merge"

	// ModePriority uses the first data source that returns a result.
	// Unlike failover, this doesn't consider health status.
	ModePriority CombineMode = "priority"

	// ModeFirst returns the result from the first data source only.
	// Other sources are used for caching/prefetching only.
	ModeFirst CombineMode = "first"
)

// CompositeSource combines multiple data sources.
type CompositeSource struct {
	// Sources is the list of data sources to combine.
	// The order matters for failover and priority modes.
	SourcesRaw []json.RawMessage `json:"sources,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies.dynamic.sources inline_key=source"`

	// Mode determines how sources are combined.
	// Default is "failover".
	Mode CombineMode `json:"mode,omitempty"`

	// Parallel enables parallel fetching from all sources.
	// Only applicable to merge and priority modes.
	Parallel bool `json:"parallel,omitempty"`

	// Timeout is the maximum time to wait for all sources.
	// Default is 5s.
	Timeout caddy.Duration `json:"timeout,omitempty"`

	// CacheResults enables caching of combined results.
	CacheResults bool `json:"cache_results,omitempty"`

	// CacheTTL is the TTL for cached results.
	// Default is 30s.
	CacheTTL caddy.Duration `json:"cache_ttl,omitempty"`

	// internal fields
	sources []datasource.DataSource
	cache   sync.Map // map[string]*cachedResult
	logger  *zap.Logger
}

// log returns the logger, defaulting to a no-op logger if nil.
func (c *CompositeSource) log() *zap.Logger {
	if c.logger == nil {
		return zap.NewNop()
	}
	return c.logger
}

type cachedResult struct {
	config    *datasource.RouteConfig
	expiresAt time.Time
}

// CaddyModule returns the Caddy module information.
func (*CompositeSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.composite",
		New: func() caddy.Module { return new(CompositeSource) },
	}
}

// Provision sets up the composite data source.
func (c *CompositeSource) Provision(ctx caddy.Context) error {
	c.logger = ctx.Logger()

	// Set defaults
	if c.Mode == "" {
		c.Mode = ModeFailover
	}
	if c.Timeout == 0 {
		c.Timeout = caddy.Duration(5 * time.Second)
	}
	if c.CacheTTL == 0 {
		c.CacheTTL = caddy.Duration(30 * time.Second)
	}

	// Load data sources
	if len(c.SourcesRaw) == 0 {
		return errors.New("at least one source is required")
	}

	c.sources = make([]datasource.DataSource, 0, len(c.SourcesRaw))
	for i, raw := range c.SourcesRaw {
		mod, err := ctx.LoadModule(&c.SourcesRaw[i], "source")
		if err != nil {
			return fmt.Errorf("loading source %d: %v", i, err)
		}
		ds, ok := mod.(datasource.DataSource)
		if !ok {
			return fmt.Errorf("source %d is not a DataSource", i)
		}
		c.sources = append(c.sources, ds)
		// Keep raw for reference
		_ = raw
	}

	c.log().Info("composite data source provisioned",
		zap.String("mode", string(c.Mode)),
		zap.Int("sources", len(c.sources)),
		zap.Bool("parallel", c.Parallel),
	)

	return nil
}

// Cleanup releases resources.
func (c *CompositeSource) Cleanup() error {
	var errs []error
	for _, src := range c.sources {
		if err := src.Cleanup(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}

// Get retrieves the route configuration using the configured combine mode.
func (c *CompositeSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	// Check cache first
	if c.CacheResults {
		if cached, ok := c.cache.Load(key); ok {
			result := cached.(*cachedResult)
			if time.Now().Before(result.expiresAt) {
				return result.config, nil
			}
			c.cache.Delete(key)
		}
	}

	var config *datasource.RouteConfig
	var err error

	switch c.Mode {
	case ModeFailover:
		config, err = c.getFailover(ctx, key)
	case ModeMerge:
		config, err = c.getMerge(ctx, key)
	case ModePriority:
		config, err = c.getPriority(ctx, key)
	case ModeFirst:
		config, err = c.getFirst(ctx, key)
	default:
		config, err = c.getFailover(ctx, key)
	}

	// Cache result
	if c.CacheResults && config != nil && err == nil {
		c.cache.Store(key, &cachedResult{
			config:    config,
			expiresAt: time.Now().Add(time.Duration(c.CacheTTL)),
		})
	}

	return config, err
}

// getFailover tries each healthy source in order until one returns a result.
func (c *CompositeSource) getFailover(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout))
	defer cancel()

	for i, src := range c.sources {
		if !src.Healthy() {
			c.log().Debug("skipping unhealthy source",
				zap.Int("source_index", i),
			)
			continue
		}

		config, err := src.Get(timeoutCtx, key)
		if err != nil {
			c.log().Debug("source returned error, trying next",
				zap.Int("source_index", i),
				zap.Error(err),
			)
			continue
		}

		if config != nil {
			c.log().Debug("got config from source",
				zap.Int("source_index", i),
				zap.String("key", key),
			)
			return config, nil
		}
	}

	return nil, nil
}

// getMerge fetches from all sources and merges the results.
func (c *CompositeSource) getMerge(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout))
	defer cancel()

	if c.Parallel {
		return c.getMergeParallel(timeoutCtx, key)
	}
	return c.getMergeSequential(timeoutCtx, key)
}

func (c *CompositeSource) getMergeSequential(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	var merged *datasource.RouteConfig

	for i, src := range c.sources {
		config, err := src.Get(ctx, key)
		if err != nil {
			c.log().Debug("source returned error during merge",
				zap.Int("source_index", i),
				zap.Error(err),
			)
			continue
		}

		if config == nil {
			continue
		}

		if merged == nil {
			merged = config.Clone()
		} else {
			c.mergeConfig(merged, config)
		}
	}

	return merged, nil
}

func (c *CompositeSource) getMergeParallel(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	type result struct {
		index  int
		config *datasource.RouteConfig
		err    error
	}

	results := make(chan result, len(c.sources))
	var wg sync.WaitGroup

	for i, src := range c.sources {
		wg.Add(1)
		go func(idx int, s datasource.DataSource) {
			defer wg.Done()
			config, err := s.Get(ctx, key)
			results <- result{index: idx, config: config, err: err}
		}(i, src)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and sort by index to maintain order
	configs := make([]*datasource.RouteConfig, len(c.sources))
	for r := range results {
		if r.err != nil {
			c.log().Debug("source returned error during parallel merge",
				zap.Int("source_index", r.index),
				zap.Error(r.err),
			)
			continue
		}
		configs[r.index] = r.config
	}

	// Merge in order
	var merged *datasource.RouteConfig
	for _, config := range configs {
		if config == nil {
			continue
		}
		if merged == nil {
			merged = config.Clone()
		} else {
			c.mergeConfig(merged, config)
		}
	}

	return merged, nil
}

// mergeConfig merges src into dst, with src taking precedence for non-empty fields.
func (c *CompositeSource) mergeConfig(dst, src *datasource.RouteConfig) {
	if src.Upstream != "" {
		dst.Upstream = src.Upstream
	}
	if src.Algorithm != "" {
		dst.Algorithm = src.Algorithm
	}
	if src.AlgorithmKey != "" {
		dst.AlgorithmKey = src.AlgorithmKey
	}
	if src.Fallback != "" {
		dst.Fallback = src.Fallback
	}
	if src.Description != "" {
		dst.Description = src.Description
	}
	if src.TTL > 0 {
		dst.TTL = src.TTL
	}
	if src.Enabled != nil {
		enabled := *src.Enabled
		dst.Enabled = &enabled
	}

	// Append rules from src
	if len(src.Rules) > 0 {
		dst.Rules = append(dst.Rules, src.Rules...)
	}

	// Merge metadata
	if len(src.Metadata) > 0 {
		if dst.Metadata == nil {
			dst.Metadata = make(map[string]string)
		}
		for k, v := range src.Metadata {
			dst.Metadata[k] = v
		}
	}

	// Use higher version
	if src.Version > dst.Version {
		dst.Version = src.Version
	}
}

// getPriority returns the first non-nil result regardless of health.
func (c *CompositeSource) getPriority(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout))
	defer cancel()

	if c.Parallel {
		return c.getPriorityParallel(timeoutCtx, key)
	}

	for i, src := range c.sources {
		config, err := src.Get(timeoutCtx, key)
		if err != nil {
			c.log().Debug("source returned error, trying next",
				zap.Int("source_index", i),
				zap.Error(err),
			)
			continue
		}
		if config != nil {
			return config, nil
		}
	}

	return nil, nil
}

func (c *CompositeSource) getPriorityParallel(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	type result struct {
		index  int
		config *datasource.RouteConfig
		err    error
	}

	results := make(chan result, len(c.sources))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for i, src := range c.sources {
		wg.Add(1)
		go func(idx int, s datasource.DataSource) {
			defer wg.Done()
			config, err := s.Get(ctx, key)
			select {
			case results <- result{index: idx, config: config, err: err}:
			case <-ctx.Done():
			}
		}(i, src)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Find the result with lowest index that has a config
	collected := make(map[int]*datasource.RouteConfig)
	received := 0
	for r := range results {
		received++
		if r.config != nil && r.err == nil {
			collected[r.index] = r.config
		}

		// Check if we have a result from the highest priority source
		for i := 0; i < len(c.sources); i++ {
			if cfg, ok := collected[i]; ok {
				return cfg, nil
			}
			// If we haven't received from this source yet, wait
			if _, ok := collected[i]; !ok && received < len(c.sources) {
				break
			}
		}
	}

	// Return highest priority result if any
	for i := 0; i < len(c.sources); i++ {
		if cfg, ok := collected[i]; ok {
			return cfg, nil
		}
	}

	return nil, nil
}

// getFirst returns only from the first source.
func (c *CompositeSource) getFirst(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	if len(c.sources) == 0 {
		return nil, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout))
	defer cancel()

	return c.sources[0].Get(timeoutCtx, key)
}

// Healthy returns true if at least one source is healthy.
func (c *CompositeSource) Healthy() bool {
	for _, src := range c.sources {
		if src.Healthy() {
			return true
		}
	}
	return false
}

// HealthStatus returns detailed health status of all sources.
func (c *CompositeSource) HealthStatus() map[int]bool {
	status := make(map[int]bool, len(c.sources))
	for i, src := range c.sources {
		status[i] = src.Healthy()
	}
	return status
}

// Interface guards
var (
	_ datasource.DataSource = (*CompositeSource)(nil)
	_ caddy.Module          = (*CompositeSource)(nil)
	_ caddy.Provisioner     = (*CompositeSource)(nil)
	_ caddy.CleanerUpper    = (*CompositeSource)(nil)
)
