// Package caddyslb provides Admin API endpoints for the dynamic load balancer.
package caddyslb

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/caddyserver/caddy/v2"
	lru "github.com/hashicorp/golang-lru"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(AdminAPI{})
}

// AdminAPI provides admin endpoints for the dynamic load balancer.
type AdminAPI struct{}

// CaddyModule returns the Caddy module information.
func (AdminAPI) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "admin.api.dynamic_lb",
		New: func() caddy.Module { return new(AdminAPI) },
	}
}

// Routes returns the admin routes for the dynamic load balancer.
func (a AdminAPI) Routes() []caddy.AdminRoute {
	return []caddy.AdminRoute{
		{
			Pattern: "/dynamic-lb/stats",
			Handler: caddy.AdminHandlerFunc(a.handleStats),
		},
		{
			Pattern: "/dynamic-lb/health",
			Handler: caddy.AdminHandlerFunc(a.handleHealth),
		},
		{
			Pattern: "/dynamic-lb/routes",
			Handler: caddy.AdminHandlerFunc(a.handleRoutes),
		},
		{
			Pattern: "/dynamic-lb/policies",
			Handler: caddy.AdminHandlerFunc(a.handlePolicies),
		},
		{
			Pattern: "/dynamic-lb/cache",
			Handler: caddy.AdminHandlerFunc(a.handleCache),
		},
	}
}

// StatsResponse contains statistics about the dynamic load balancer.
type StatsResponse struct {
	RouteHits    map[string]int64 `json:"route_hits,omitempty"`
	RouteMisses  map[string]int64 `json:"route_misses,omitempty"`
	CacheHits    uint64           `json:"cache_hits"`
	CacheMisses  uint64           `json:"cache_misses"`
	CacheHitRate float64          `json:"cache_hit_rate"`
}

// HealthResponse contains health information about data sources.
type HealthResponse struct {
	DataSources map[string]DataSourceHealthInfo              `json:"data_sources"`
	Overall     string                                       `json:"overall"`
	ParseErrors map[string]metrics.RouteConfigParseErrorInfo `json:"route_config_parse_errors,omitempty"`
}

// DataSourceHealthInfo contains health info for a single data source.
type DataSourceHealthInfo struct {
	Healthy     bool   `json:"healthy"`
	Type        string `json:"type"`
	ConfigCount int    `json:"config_count"`
	LastError   string `json:"last_error,omitempty"`
}

// RoutesResponse contains route configuration information.
type RoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
	Total  int         `json:"total"`
}

// RouteInfo contains information about a single route.
type RouteInfo struct {
	Source     string   `json:"source,omitempty"`
	SourceType string   `json:"source_type,omitempty"`
	Key        string   `json:"key"`
	Upstream   string   `json:"upstream,omitempty"`
	Upstreams  []string `json:"upstreams,omitempty"`
	RuleCount  int      `json:"rule_count"`
}

// CacheResponse contains cache statistics.
type CacheResponse struct {
	Entries      int                                          `json:"entries"`
	HitRate      float64                                      `json:"hit_rate"`
	MaxSize      int                                          `json:"max_size"`
	Hits         uint64                                       `json:"hits"`
	Misses       uint64                                       `json:"misses"`
	NegativeHits uint64                                       `json:"negative_hits"`
	Sources      map[string]datasource.AdminCacheStats        `json:"sources,omitempty"`
	ParseErrors  map[string]metrics.RouteConfigParseErrorInfo `json:"route_config_parse_errors,omitempty"`
}

// PoliciesResponse contains registered dynamic selection policy instances.
type PoliciesResponse struct {
	Policies []SelectionPolicyInfo `json:"policies"`
	Total    int                   `json:"total"`
}

// handleStats returns statistics about route hits/misses.
func (a *AdminAPI) handleStats(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        nil,
		}
	}

	// Route hit/miss stats (recorded by the selection policy)
	stats := routeStatsRegistry.GetStats()

	// Cache stats aggregated across registered data sources
	entries := datasource.ListAdminEntries()
	cacheHits, cacheMisses := uint64(0), uint64(0)
	for _, e := range entries {
		cs := e.DS.AdminCacheStats()
		cacheHits += cs.Hits
		cacheMisses += cs.Misses
	}
	cacheTotal := cacheHits + cacheMisses
	cacheHitRate := 0.0
	if cacheTotal > 0 {
		cacheHitRate = float64(cacheHits) / float64(cacheTotal)
	}

	response := StatsResponse{
		RouteHits:    stats.Hits,
		RouteMisses:  stats.Misses,
		CacheHits:    cacheHits,
		CacheMisses:  cacheMisses,
		CacheHitRate: cacheHitRate,
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// handleHealth returns health status of data sources.
func (a *AdminAPI) handleHealth(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        nil,
		}
	}

	entries := datasource.ListAdminEntries()
	info := make(map[string]DataSourceHealthInfo, len(entries))
	overall := "healthy"
	for _, e := range entries {
		routes := e.DS.AdminListRoutes()
		healthy := e.DS.Healthy()
		lastErr := e.DS.AdminLastError()
		info[e.Name] = DataSourceHealthInfo{
			Healthy:     healthy,
			Type:        e.Type,
			ConfigCount: len(routes),
			LastError:   lastErr,
		}
		if !healthy {
			overall = "degraded"
		}
	}

	response := HealthResponse{DataSources: info, Overall: overall, ParseErrors: metrics.RouteConfigParseErrorSnapshot()}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// handleRoutes returns route configuration information.
func (a *AdminAPI) handleRoutes(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        nil,
		}
	}

	entries := datasource.ListAdminEntries()
	routes := make([]RouteInfo, 0)
	for _, e := range entries {
		for _, ri := range e.DS.AdminListRoutes() {
			routes = append(routes, RouteInfo{
				Source:     e.Name,
				SourceType: e.Type,
				Key:        ri.Key,
				Upstream:   ri.Upstream,
				Upstreams:  ri.Upstreams,
				RuleCount:  ri.RuleCount,
			})
		}
	}

	response := RoutesResponse{Routes: routes, Total: len(routes)}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// handleCache returns cache statistics.
func (a *AdminAPI) handleCache(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		return a.getCacheStats(w, r)
	case http.MethodDelete:
		return a.clearCache(w, r)
	default:
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        nil,
		}
	}
}

// handlePolicies lists registered dynamic selection policy instances.
func (a *AdminAPI) handlePolicies(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{HTTPStatus: http.StatusMethodNotAllowed, Err: nil}
	}

	policies := listSelectionPolicies()
	response := PoliciesResponse{Policies: policies, Total: len(policies)}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

func (a *AdminAPI) getCacheStats(w http.ResponseWriter, _ *http.Request) error {
	entries := datasource.ListAdminEntries()
	perSource := make(map[string]datasource.AdminCacheStats, len(entries))

	totalEntries := 0
	totalMax := 0
	hits := uint64(0)
	misses := uint64(0)
	negativeHits := uint64(0)
	for _, e := range entries {
		cs := e.DS.AdminCacheStats()
		perSource[e.Name] = cs
		totalEntries += cs.Entries
		totalMax += cs.MaxSize
		hits += cs.Hits
		misses += cs.Misses
		negativeHits += cs.NegativeHits
	}

	hitRate := 0.0
	if hits+misses > 0 {
		hitRate = float64(hits) / float64(hits+misses)
	}

	stats := CacheResponse{
		Entries:      totalEntries,
		MaxSize:      totalMax,
		Hits:         hits,
		Misses:       misses,
		NegativeHits: negativeHits,
		HitRate:      hitRate,
		Sources:      perSource,
		ParseErrors:  metrics.RouteConfigParseErrorSnapshot(),
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(stats)
}

func (a *AdminAPI) clearCache(w http.ResponseWriter, _ *http.Request) error {
	entries := datasource.ListAdminEntries()
	cleared := 0
	for _, e := range entries {
		e.DS.AdminClearCache()
		cleared++
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"status":          "cache cleared",
		"cleared_sources": cleared,
	})
}

// RouteStats holds route hit/miss statistics.
type RouteStats struct {
	Hits   map[string]int64
	Misses map[string]int64
}

// RouteStatsRegistry is a thread-safe registry for route statistics.
type RouteStatsRegistry struct {
	mu     sync.RWMutex
	hits   *lru.Cache
	misses *lru.Cache
}

const defaultRouteStatsMaxKeys = 1000

// NewRouteStatsRegistry creates a new RouteStatsRegistry.
func NewRouteStatsRegistry() *RouteStatsRegistry {
	hits, _ := lru.New(defaultRouteStatsMaxKeys)
	misses, _ := lru.New(defaultRouteStatsMaxKeys)
	return &RouteStatsRegistry{
		hits:   hits,
		misses: misses,
	}
}

// RecordHit records a route hit.
func (r *RouteStatsRegistry) RecordHit(key string, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == "" {
		return
	}
	if v, ok := r.hits.Get(key); ok {
		r.hits.Add(key, v.(int64)+1)
		return
	}
	r.hits.Add(key, int64(1))
}

// RecordMiss records a route miss.
func (r *RouteStatsRegistry) RecordMiss(key string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = reason
	if key == "" {
		return
	}
	if v, ok := r.misses.Get(key); ok {
		r.misses.Add(key, v.(int64)+1)
		return
	}
	r.misses.Add(key, int64(1))
}

// GetStats returns a copy of the current statistics.
func (r *RouteStatsRegistry) GetStats() RouteStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hits := make(map[string]int64, r.hits.Len())
	for _, k := range r.hits.Keys() {
		key, ok := k.(string)
		if !ok {
			continue
		}
		if v, ok := r.hits.Peek(key); ok {
			hits[key] = v.(int64)
		}
	}

	misses := make(map[string]int64, r.misses.Len())
	for _, k := range r.misses.Keys() {
		key, ok := k.(string)
		if !ok {
			continue
		}
		if v, ok := r.misses.Peek(key); ok {
			misses[key] = v.(int64)
		}
	}

	return RouteStats{
		Hits:   hits,
		Misses: misses,
	}
}

// Global registries
var (
	routeStatsRegistry = NewRouteStatsRegistry()
)

// Interface guards
var (
	_ caddy.AdminRouter = (*AdminAPI)(nil)
)
