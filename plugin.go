// Package caddyslb provides a dynamic selection policy for Caddy's reverse proxy.
package caddyslb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/extractor"
	"github.com/puxu-msft/caddy-dynamic-routing/matcher"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"

	// Import data sources to register them
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/consul"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/etcd"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/file"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/http"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/kubernetes"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/redis"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/sql"
	_ "github.com/puxu-msft/caddy-dynamic-routing/datasource/zookeeper"
)

func init() {
	caddy.RegisterModule(DynamicSelection{})
}

// DynamicSelection is a selection policy that routes requests based on
// dynamic configuration from external data sources like etcd.
//
// It extracts a routing key from the request using placeholder expressions,
// looks up the routing configuration from the data source, and selects
// the appropriate upstream based on matching rules.
type DynamicSelection struct {
	// Key is the placeholder expression used to extract the routing key from requests.
	// Examples:
	//   - "{http.request.header.X-Tenant}" - use X-Tenant header
	//   - "{header.X-Org}-{cookie.region}" - composite key
	//   - "{query.tenant}" - use query parameter
	Key string `json:"key,omitempty"`

	// DataSourceRaw is the raw JSON configuration for the data source.
	// The data source must implement the datasource.DataSource interface.
	DataSourceRaw json.RawMessage `json:"data_source,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies.dynamic.sources inline_key=source"`

	// FallbackRaw is the raw JSON configuration for the fallback selection policy.
	// Used when no routing configuration is found or the data source is unavailable.
	FallbackRaw json.RawMessage `json:"fallback,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies inline_key=policy"`

	// Internal fields
	keyExtractor       extractor.KeyExtractor
	dataSource         datasource.DataSource
	fallback           reverseproxy.Selector
	ruleMatcher        *matcher.RuleMatcher
	logger             *zap.Logger
	dataSourceTypeName string // Cached for metrics (computed in Provision)

	// Pool index for O(1) upstream lookup (built lazily)
	poolIndex    map[string]*reverseproxy.Upstream
	lastPoolSize int // Track pool size for cache invalidation
}

// CaddyModule returns the Caddy module information.
func (DynamicSelection) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic",
		New: func() caddy.Module { return new(DynamicSelection) },
	}
}

// Provision sets up the dynamic selection policy.
func (s *DynamicSelection) Provision(ctx caddy.Context) error {
	s.logger = ctx.Logger()
	s.ruleMatcher = matcher.NewRuleMatcher()

	// Parse key expression
	if s.Key != "" {
		var err error
		s.keyExtractor, err = extractor.NewFromExpression(s.Key)
		if err != nil {
			return fmt.Errorf("parsing key expression: %v", err)
		}
	}

	// Load data source module
	if s.DataSourceRaw != nil {
		val, err := ctx.LoadModule(s, "DataSourceRaw")
		if err != nil {
			return fmt.Errorf("loading data source: %v", err)
		}
		s.dataSource = val.(datasource.DataSource)
		// Pre-compute data source type name for metrics
		s.dataSourceTypeName = s.computeDataSourceType()
	}

	// Load fallback selector
	if s.FallbackRaw != nil {
		val, err := ctx.LoadModule(s, "FallbackRaw")
		if err != nil {
			return fmt.Errorf("loading fallback policy: %v", err)
		}
		s.fallback = val.(reverseproxy.Selector)
	} else {
		// Default to random selection
		s.fallback = new(reverseproxy.RandomSelection)
	}

	s.logger.Info("dynamic selection policy provisioned",
		zap.String("key", s.Key),
		zap.Bool("has_datasource", s.dataSource != nil),
	)

	return nil
}

// Cleanup releases resources.
func (s *DynamicSelection) Cleanup() error {
	// Data source cleanup is handled by its own CleanerUpper implementation
	return nil
}

// Select selects an upstream from the pool based on dynamic routing configuration.
func (s *DynamicSelection) Select(pool reverseproxy.UpstreamPool, r *http.Request, w http.ResponseWriter) *reverseproxy.Upstream {
	// If no pool, nothing to select
	if len(pool) == 0 {
		return nil
	}

	// Get the replacer from the request context with nil check
	replVal := r.Context().Value(caddy.ReplacerCtxKey)
	if replVal == nil {
		s.logger.Debug("no replacer in context, using fallback")
		metrics.RecordRouteMiss("", metrics.MissReasonNoReplacer)
		return s.fallback.Select(pool, r, w)
	}
	repl, ok := replVal.(*caddy.Replacer)
	if !ok || repl == nil {
		s.logger.Debug("invalid replacer type in context, using fallback")
		metrics.RecordRouteMiss("", metrics.MissReasonNoReplacer)
		return s.fallback.Select(pool, r, w)
	}

	// If no key extractor or data source, use fallback
	if s.keyExtractor == nil || s.dataSource == nil {
		if s.keyExtractor == nil {
			metrics.RecordRouteMiss("", metrics.MissReasonNoKeyExtractor)
		} else {
			metrics.RecordRouteMiss("", metrics.MissReasonNoDataSource)
		}
		return s.fallback.Select(pool, r, w)
	}

	// Extract routing key from request
	key := s.keyExtractor.Extract(r, repl)
	if key == "" {
		s.logger.Debug("no routing key extracted, using fallback")
		metrics.RecordRouteMiss("", metrics.MissReasonNoKey)
		return s.fallback.Select(pool, r, w)
	}

	// Check data source health
	if !s.dataSource.Healthy() {
		s.logger.Debug("data source unhealthy, using fallback",
			zap.String("key", key),
		)
		metrics.RecordRouteMiss(key, metrics.MissReasonUnhealthy)
		return s.fallback.Select(pool, r, w)
	}

	// Get routing configuration with timing
	timer := metrics.NewTimer()
	config, err := s.dataSource.Get(r.Context(), key)
	timer.ObserveDuration(metrics.DataSourceLatency.WithLabelValues(s.dataSourceType()))

	if err != nil {
		s.logger.Warn("failed to get route config",
			zap.String("key", key),
			zap.Error(err),
		)
		metrics.RecordRouteMiss(key, metrics.MissReasonError)
		return s.fallback.Select(pool, r, w)
	}

	if config == nil {
		s.logger.Debug("no route config found",
			zap.String("key", key),
		)
		metrics.RecordRouteMiss(key, metrics.MissReasonNoConfig)
		return s.fallback.Select(pool, r, w)
	}

	// Match rules and get target upstream with timing
	matchStart := time.Now()
	targetUpstream := s.ruleMatcher.Match(r, repl, config)
	metrics.RuleMatchLatency.Observe(time.Since(matchStart).Seconds())

	if targetUpstream == nil {
		s.logger.Debug("no matching rule found",
			zap.String("key", key),
		)
		metrics.RecordRouteMiss(key, metrics.MissReasonNoMatch)
		return s.fallback.Select(pool, r, w)
	}

	// Find the upstream in the pool
	upstream := s.findUpstreamInPool(pool, targetUpstream.Address)
	if upstream == nil {
		s.logger.Debug("matched upstream not in pool",
			zap.String("key", key),
			zap.String("target", targetUpstream.Address),
		)
		metrics.RecordRouteMiss(key, metrics.MissReasonNotInPool)
		return s.fallback.Select(pool, r, w)
	}

	s.logger.Debug("selected upstream via dynamic routing",
		zap.String("key", key),
		zap.String("upstream", targetUpstream.Address),
	)

	// Record successful route hit
	metrics.RecordRouteHit(key, targetUpstream.Address)

	return upstream
}

// dataSourceType returns the cached data source type name for metrics.
func (s *DynamicSelection) dataSourceType() string {
	if s.dataSourceTypeName != "" {
		return s.dataSourceTypeName
	}
	return "unknown"
}

// computeDataSourceType extracts the data source type from module ID.
func (s *DynamicSelection) computeDataSourceType() string {
	if s.dataSource == nil {
		return "unknown"
	}
	info := s.dataSource.CaddyModule()
	// Extract the last part of the module ID (e.g., "etcd" from "...sources.etcd")
	id := string(info.ID)
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}

// findUpstreamInPool finds an upstream in the pool by its dial address.
// Uses an indexed map for O(1) lookup instead of O(n) linear search.
func (s *DynamicSelection) findUpstreamInPool(pool reverseproxy.UpstreamPool, address string) *reverseproxy.Upstream {
	// Rebuild index if pool size changed (simple invalidation strategy)
	if s.poolIndex == nil || len(pool) != s.lastPoolSize {
		s.poolIndex = make(map[string]*reverseproxy.Upstream, len(pool))
		for _, upstream := range pool {
			s.poolIndex[upstream.Dial] = upstream
		}
		s.lastPoolSize = len(pool)
	}
	return s.poolIndex[address]
}

// Interface guards
var (
	_ caddy.Module          = (*DynamicSelection)(nil)
	_ caddy.Provisioner     = (*DynamicSelection)(nil)
	_ caddy.CleanerUpper    = (*DynamicSelection)(nil)
	_ reverseproxy.Selector = (*DynamicSelection)(nil)
)
