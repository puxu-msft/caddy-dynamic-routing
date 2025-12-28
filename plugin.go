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

	// InstanceKey is the placeholder expression used to extract an instance-specific routing key.
	// When set, the policy will prefer instance-level routing first, then fall back.
	InstanceKey string `json:"instance_key,omitempty"`

	// VersionKey is the placeholder expression used to extract a version-level routing key.
	// Used as fallback when InstanceKey is set but does not yield an available upstream.
	VersionKey string `json:"version_key,omitempty"`

	// DataSourceRaw is the raw JSON configuration for the data source.
	// The data source must implement the datasource.DataSource interface.
	DataSourceRaw json.RawMessage `json:"data_source,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies.dynamic.sources inline_key=source"`

	// FallbackRaw is the raw JSON configuration for the fallback selection policy.
	// Used when no routing configuration is found or the data source is unavailable.
	FallbackRaw json.RawMessage `json:"fallback,omitempty" caddy:"namespace=http.reverse_proxy.selection_policies inline_key=policy"`

	// MetricsCardinality controls the cardinality of route hit/miss metrics.
	// Allowed values:
	//   - "detailed" (default): include routing key and upstream labels
	//   - "coarse": collapse key/upstream labels to a constant to bound time series
	MetricsCardinality string `json:"metrics_cardinality,omitempty"`

	// Internal fields
	keyExtractor         extractor.KeyExtractor
	instanceKeyExtractor extractor.KeyExtractor
	versionKeyExtractor  extractor.KeyExtractor
	dataSource           datasource.DataSource
	fallback             reverseproxy.Selector
	ruleMatcher          *matcher.RuleMatcher
	logger               *zap.Logger
	dataSourceTypeName   string // Cached for metrics (computed in Provision)
	isUpstreamAvailable  func(*reverseproxy.Upstream) bool

	// Pool index for O(1) upstream lookup (built lazily)
	poolIndex    map[string]*reverseproxy.Upstream
	lastPoolSize int // Track pool size for cache invalidation

	adminName string
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
	if s.isUpstreamAvailable == nil {
		s.isUpstreamAvailable = func(u *reverseproxy.Upstream) bool { return u.Available() }
	}

	if s.MetricsCardinality != "" {
		if err := metrics.SetRouteMetricsCardinalityFromString(s.MetricsCardinality); err != nil {
			return fmt.Errorf("invalid metrics_cardinality: %v", err)
		}
	}

	// Parse key expressions
	if s.Key != "" {
		var err error
		s.keyExtractor, err = extractor.NewFromExpression(s.Key)
		if err != nil {
			return fmt.Errorf("parsing key expression: %v", err)
		}
	}
	if s.InstanceKey != "" {
		var err error
		s.instanceKeyExtractor, err = extractor.NewFromExpression(s.InstanceKey)
		if err != nil {
			return fmt.Errorf("parsing instance_key expression: %v", err)
		}
	}
	if s.VersionKey != "" {
		var err error
		s.versionKeyExtractor, err = extractor.NewFromExpression(s.VersionKey)
		if err != nil {
			return fmt.Errorf("parsing version_key expression: %v", err)
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

	// Register this policy instance for Admin API inspection.
	// Note: this registry is best-effort for debugging/ops and does not affect routing behavior.
	s.adminName = registerSelectionPolicy(SelectionPolicyInfo{
		ModuleID:           "http.reverse_proxy.selection_policies.dynamic",
		Key:                s.Key,
		InstanceKey:        s.InstanceKey,
		VersionKey:         s.VersionKey,
		DataSourceType:     s.dataSourceTypeForAdmin(),
		DataSourceModuleID: s.dataSourceModuleID(),
		FallbackPolicy:     s.fallbackPolicyID(),
	})

	s.logger.Info("dynamic selection policy provisioned",
		zap.String("key", s.Key),
		zap.String("instance_key", s.InstanceKey),
		zap.String("version_key", s.VersionKey),
		zap.Bool("has_datasource", s.dataSource != nil),
	)

	return nil
}

// Cleanup releases resources.
func (s *DynamicSelection) Cleanup() error {
	unregisterSelectionPolicy(s.adminName)
	// Data source cleanup is handled by its own CleanerUpper implementation
	return nil
}

func (s *DynamicSelection) dataSourceTypeForAdmin() string {
	if s.dataSource == nil {
		return ""
	}
	// Prefer the cached/parsed type name.
	if s.dataSourceTypeName != "" && s.dataSourceTypeName != "unknown" {
		return s.dataSourceTypeName
	}
	return s.computeDataSourceType()
}

func (s *DynamicSelection) fallbackPolicyID() string {
	if s.fallback == nil {
		return ""
	}
	if mod, ok := s.fallback.(caddy.Module); ok {
		return string(mod.CaddyModule().ID)
	}
	return fmt.Sprintf("%T", s.fallback)
}

func (s *DynamicSelection) dataSourceModuleID() string {
	if s.dataSource == nil {
		return ""
	}
	if mod, ok := s.dataSource.(caddy.Module); ok {
		return string(mod.CaddyModule().ID)
	}
	return fmt.Sprintf("%T", s.dataSource)
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
		routeStatsRegistry.RecordMiss("", string(metrics.MissReasonNoReplacer))
		return s.fallback.Select(pool, r, w)
	}
	repl, ok := replVal.(*caddy.Replacer)
	if !ok || repl == nil {
		s.logger.Debug("invalid replacer type in context, using fallback")
		metrics.RecordRouteMiss("", metrics.MissReasonNoReplacer)
		routeStatsRegistry.RecordMiss("", string(metrics.MissReasonNoReplacer))
		return s.fallback.Select(pool, r, w)
	}

	// If no data source, use fallback
	if s.dataSource == nil {
		metrics.RecordRouteMiss("", metrics.MissReasonNoDataSource)
		routeStatsRegistry.RecordMiss("", string(metrics.MissReasonNoDataSource))
		return s.fallback.Select(pool, r, w)
	}

	// Determine routing mode.
	useInstanceVersion := s.instanceKeyExtractor != nil || s.versionKeyExtractor != nil
	if !useInstanceVersion && s.keyExtractor == nil {
		metrics.RecordRouteMiss("", metrics.MissReasonNoKeyExtractor)
		routeStatsRegistry.RecordMiss("", string(metrics.MissReasonNoKeyExtractor))
		return s.fallback.Select(pool, r, w)
	}
	if useInstanceVersion && s.instanceKeyExtractor == nil && s.versionKeyExtractor == nil {
		metrics.RecordRouteMiss("", metrics.MissReasonNoKeyExtractor)
		routeStatsRegistry.RecordMiss("", string(metrics.MissReasonNoKeyExtractor))
		return s.fallback.Select(pool, r, w)
	}

	// Helper: attempt routing for a given key extractor.
	attempt := func(keyExt extractor.KeyExtractor) (key string, chosenAddr string, chosen *reverseproxy.Upstream, miss metrics.MissReason, ok bool) {
		if keyExt == nil {
			return "", "", nil, metrics.MissReasonNoKeyExtractor, false
		}
		key = keyExt.Extract(r, repl)
		if key == "" {
			return "", "", nil, metrics.MissReasonNoKey, false
		}

		dsHealthy := s.dataSource.Healthy()
		timer := metrics.NewTimer()
		config, err := s.dataSource.Get(r.Context(), key)
		timer.ObserveDuration(metrics.DataSourceLatency.WithLabelValues(s.dataSourceType()))
		if err != nil {
			return key, "", nil, metrics.MissReasonError, false
		}
		if config == nil {
			if !dsHealthy {
				return key, "", nil, metrics.MissReasonUnhealthy, false
			}
			return key, "", nil, metrics.MissReasonNoConfig, false
		}
		if !config.IsEnabled() {
			return key, "", nil, metrics.MissReasonDisabled, false
		}
		if config.IsExpired() {
			return key, "", nil, metrics.MissReasonExpired, false
		}

		matchStart := time.Now()
		matchRes := s.ruleMatcher.MatchResult(r, repl, config)
		metrics.RuleMatchLatency.Observe(time.Since(matchStart).Seconds())
		if matchRes == nil {
			return key, "", nil, metrics.MissReasonNoMatch, false
		}

		inPool := make([]datasource.WeightedUpstream, 0, len(matchRes.Upstreams))
		available := make([]datasource.WeightedUpstream, 0, len(matchRes.Upstreams))
		for _, cand := range matchRes.Upstreams {
			u := s.findUpstreamInPool(pool, cand.Address)
			if u == nil {
				continue
			}
			inPool = append(inPool, cand)
			if s.isUpstreamAvailable != nil {
				if !s.isUpstreamAvailable(u) {
					continue
				}
			}
			available = append(available, cand)
		}
		if len(inPool) == 0 {
			return key, "", nil, metrics.MissReasonNotInPool, false
		}
		if len(available) == 0 {
			return key, "", nil, metrics.MissReasonNoAvailableUpstream, false
		}

		selected := s.ruleMatcher.SelectFromUpstreams(r, available, matchRes.Algorithm, matchRes.AlgorithmKey)
		if selected == nil {
			return key, "", nil, metrics.MissReasonNoMatch, false
		}
		chosen = s.findUpstreamInPool(pool, selected.Address)
		if chosen == nil {
			return key, "", nil, metrics.MissReasonNotInPool, false
		}
		return key, selected.Address, chosen, "", true
	}

	// Multi-key mode: instance-prefer then version fallback.
	if useInstanceVersion {
		var instanceFail metrics.MissReason
		instanceKey, instAddr, instUp, instMiss, instOK := attempt(s.instanceKeyExtractor)
		if instOK {
			s.logger.Debug("selected upstream via dynamic routing (instance)", zap.String("key", instanceKey), zap.String("upstream", instAddr))
			metrics.RecordRouteHit(instanceKey, instAddr)
			routeStatsRegistry.RecordHit(instanceKey, instAddr)
			return instUp
		}
		instanceFail = instMiss

		versionKey, verAddr, verUp, verMiss, verOK := attempt(s.versionKeyExtractor)
		if verOK {
			metrics.RecordInstanceFallback(instanceFail)
			s.logger.Debug("selected upstream via dynamic routing (version)", zap.String("key", versionKey), zap.String("upstream", verAddr))
			metrics.RecordRouteHit(versionKey, verAddr)
			routeStatsRegistry.RecordHit(versionKey, verAddr)
			return verUp
		}

		// Both attempts failed; attribute miss to the last attempted key if present.
		missKey := versionKey
		missReason := verMiss
		if missKey == "" {
			missKey = instanceKey
			missReason = instanceFail
		}
		metrics.RecordRouteMiss(missKey, missReason)
		routeStatsRegistry.RecordMiss(missKey, string(missReason))
		return s.fallback.Select(pool, r, w)
	}

	key, addr, upstream, miss, ok := attempt(s.keyExtractor)
	if !ok {
		metrics.RecordRouteMiss(key, miss)
		routeStatsRegistry.RecordMiss(key, string(miss))
		return s.fallback.Select(pool, r, w)
	}

	s.logger.Debug("selected upstream via dynamic routing",
		zap.String("key", key),
		zap.String("upstream", addr),
	)
	metrics.RecordRouteHit(key, addr)
	routeStatsRegistry.RecordHit(key, addr)
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
