// Package caddyslb provides Admin API endpoints for the dynamic load balancer.
package caddyslb

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/caddyserver/caddy/v2"

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
			Pattern: "/dynamic-lb/cache",
			Handler: caddy.AdminHandlerFunc(a.handleCache),
		},
	}
}

// StatsResponse contains statistics about the dynamic load balancer.
type StatsResponse struct {
	RouteHits   map[string]int64 `json:"route_hits,omitempty"`
	RouteMisses map[string]int64 `json:"route_misses,omitempty"`
	CacheHits   int64            `json:"cache_hits"`
	CacheMisses int64            `json:"cache_misses"`
	CacheHitRate float64         `json:"cache_hit_rate"`
}

// HealthResponse contains health information about data sources.
type HealthResponse struct {
	DataSources map[string]DataSourceHealthInfo `json:"data_sources"`
	Overall     string                          `json:"overall"`
}

// DataSourceHealthInfo contains health info for a single data source.
type DataSourceHealthInfo struct {
	Healthy      bool   `json:"healthy"`
	Type         string `json:"type"`
	ConfigCount  int    `json:"config_count"`
	LastError    string `json:"last_error,omitempty"`
}

// RoutesResponse contains route configuration information.
type RoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
	Total  int         `json:"total"`
}

// RouteInfo contains information about a single route.
type RouteInfo struct {
	Key       string   `json:"key"`
	Upstream  string   `json:"upstream,omitempty"`
	Upstreams []string `json:"upstreams,omitempty"`
	RuleCount int      `json:"rule_count"`
}

// CacheResponse contains cache statistics.
type CacheResponse struct {
	Entries  int     `json:"entries"`
	HitRate  float64 `json:"hit_rate"`
	MaxSize  int     `json:"max_size"`
}

// handleStats returns statistics about route hits/misses.
func (a *AdminAPI) handleStats(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        nil,
		}
	}

	// Get stats from registry
	stats := routeStatsRegistry.GetStats()

	response := StatsResponse{
		RouteHits:    stats.Hits,
		RouteMisses:  stats.Misses,
		CacheHits:    stats.CacheHits,
		CacheMisses:  stats.CacheMisses,
		CacheHitRate: stats.CacheHitRate(),
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

	// Get health info from registry
	healthInfo := dataSourceRegistry.GetHealthInfo()

	overall := "healthy"
	for _, info := range healthInfo {
		if !info.Healthy {
			overall = "degraded"
			break
		}
	}

	response := HealthResponse{
		DataSources: healthInfo,
		Overall:     overall,
	}

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

	// Get routes from registry
	routes := dataSourceRegistry.GetRoutes()

	response := RoutesResponse{
		Routes: routes,
		Total:  len(routes),
	}

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

func (a *AdminAPI) getCacheStats(w http.ResponseWriter, _ *http.Request) error {
	stats := dataSourceRegistry.GetCacheStats()

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(stats)
}

func (a *AdminAPI) clearCache(w http.ResponseWriter, _ *http.Request) error {
	dataSourceRegistry.ClearCache()

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]string{
		"status": "cache cleared",
	})
}

// RouteStats holds route hit/miss statistics.
type RouteStats struct {
	Hits        map[string]int64
	Misses      map[string]int64
	CacheHits   int64
	CacheMisses int64
}

// CacheHitRate calculates the cache hit rate.
func (s *RouteStats) CacheHitRate() float64 {
	total := s.CacheHits + s.CacheMisses
	if total == 0 {
		return 0.0
	}
	return float64(s.CacheHits) / float64(total)
}

// RouteStatsRegistry is a thread-safe registry for route statistics.
type RouteStatsRegistry struct {
	mu          sync.RWMutex
	hits        map[string]int64
	misses      map[string]int64
	cacheHits   int64
	cacheMisses int64
}

// NewRouteStatsRegistry creates a new RouteStatsRegistry.
func NewRouteStatsRegistry() *RouteStatsRegistry {
	return &RouteStatsRegistry{
		hits:   make(map[string]int64),
		misses: make(map[string]int64),
	}
}

// RecordHit records a route hit.
func (r *RouteStatsRegistry) RecordHit(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits[key]++

	// Also record to Prometheus
	metrics.RecordRouteHit(key, "")
}

// RecordMiss records a route miss.
func (r *RouteStatsRegistry) RecordMiss(key string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.misses[key]++

	// Also record to Prometheus
	metrics.RecordRouteMiss(key, metrics.MissReason(reason))
}

// RecordCacheHit records a cache hit.
func (r *RouteStatsRegistry) RecordCacheHit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheHits++
}

// RecordCacheMiss records a cache miss.
func (r *RouteStatsRegistry) RecordCacheMiss() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheMisses++
}

// GetStats returns a copy of the current statistics.
func (r *RouteStatsRegistry) GetStats() RouteStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hits := make(map[string]int64, len(r.hits))
	for k, v := range r.hits {
		hits[k] = v
	}

	misses := make(map[string]int64, len(r.misses))
	for k, v := range r.misses {
		misses[k] = v
	}

	return RouteStats{
		Hits:        hits,
		Misses:      misses,
		CacheHits:   r.cacheHits,
		CacheMisses: r.cacheMisses,
	}
}

// DataSourceRegistry tracks registered data sources.
type DataSourceRegistry struct {
	mu          sync.RWMutex
	dataSources map[string]DataSourceEntry
}

// DataSourceEntry holds information about a registered data source.
type DataSourceEntry struct {
	Source    interface{}
	Type      string
	Healthy   bool
	LastError string
}

// NewDataSourceRegistry creates a new DataSourceRegistry.
func NewDataSourceRegistry() *DataSourceRegistry {
	return &DataSourceRegistry{
		dataSources: make(map[string]DataSourceEntry),
	}
}

// Register registers a data source.
func (d *DataSourceRegistry) Register(name string, source interface{}, sourceType string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dataSources[name] = DataSourceEntry{
		Source:  source,
		Type:    sourceType,
		Healthy: true,
	}
}

// Unregister removes a data source.
func (d *DataSourceRegistry) Unregister(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.dataSources, name)
}

// UpdateHealth updates the health status of a data source.
func (d *DataSourceRegistry) UpdateHealth(name string, healthy bool, lastError string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if entry, ok := d.dataSources[name]; ok {
		entry.Healthy = healthy
		entry.LastError = lastError
		d.dataSources[name] = entry
	}
}

// GetHealthInfo returns health information for all data sources.
func (d *DataSourceRegistry) GetHealthInfo() map[string]DataSourceHealthInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]DataSourceHealthInfo, len(d.dataSources))
	for name, entry := range d.dataSources {
		result[name] = DataSourceHealthInfo{
			Healthy:   entry.Healthy,
			Type:      entry.Type,
			LastError: entry.LastError,
		}
	}
	return result
}

// GetRoutes returns route information from all data sources.
func (d *DataSourceRegistry) GetRoutes() []RouteInfo {
	// This is a simplified implementation
	// In a real implementation, this would iterate over cached routes
	return []RouteInfo{}
}

// GetCacheStats returns cache statistics.
func (d *DataSourceRegistry) GetCacheStats() CacheResponse {
	// This is a simplified implementation
	return CacheResponse{
		Entries: 0,
		HitRate: 0.0,
		MaxSize: 0,
	}
}

// ClearCache clears all caches.
func (d *DataSourceRegistry) ClearCache() {
	// This is a simplified implementation
	// In a real implementation, this would clear caches in all data sources
}

// Global registries
var (
	routeStatsRegistry   = NewRouteStatsRegistry()
	dataSourceRegistry   = NewDataSourceRegistry()
)

// Interface guards
var (
	_ caddy.AdminRouter = (*AdminAPI)(nil)
)
