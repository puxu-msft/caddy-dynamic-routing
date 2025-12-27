package caddyslb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2"

	ds "github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

type testAdminDS struct {
	healthy     bool
	lastErr     string
	routes      []ds.AdminRouteInfo
	cache       ds.AdminCacheStats
	clearCalled int
}

func (*testAdminDS) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "test.datasource",
		New: func() caddy.Module {
			return new(testAdminDS)
		},
	}
}

func (*testAdminDS) Provision(caddy.Context) error { return nil }
func (*testAdminDS) Cleanup() error                { return nil }

func (*testAdminDS) Get(context.Context, string) (*ds.RouteConfig, error) { return nil, nil }
func (t *testAdminDS) Healthy() bool                                      { return t.healthy }

func (*testAdminDS) AdminType() string                      { return "test" }
func (t *testAdminDS) AdminListRoutes() []ds.AdminRouteInfo { return t.routes }
func (t *testAdminDS) AdminCacheStats() ds.AdminCacheStats  { return t.cache }
func (t *testAdminDS) AdminClearCache() {
	t.clearCalled++
	t.cache.Entries = 0
	t.cache.Hits = 0
	t.cache.Misses = 0
	t.cache.NegativeHits = 0
	t.cache.HitRate = 0
}
func (t *testAdminDS) AdminLastError() string { return t.lastErr }

func cleanupAdminRegistry() {
	for _, e := range ds.ListAdminEntries() {
		ds.UnregisterAdminSource(e.Name)
	}
}

func cleanupSelectionPolicyRegistry() {
	for _, p := range listSelectionPolicies() {
		unregisterSelectionPolicy(p.Name)
	}
}

func TestAdminAPI_Routes(t *testing.T) {
	api := AdminAPI{}
	routes := api.Routes()

	if len(routes) != 5 {
		t.Errorf("Expected 5 routes, got %d", len(routes))
	}

	expectedPatterns := []string{
		"/dynamic-lb/stats",
		"/dynamic-lb/health",
		"/dynamic-lb/routes",
		"/dynamic-lb/policies",
		"/dynamic-lb/cache",
	}

	for i, pattern := range expectedPatterns {
		if routes[i].Pattern != pattern {
			t.Errorf("Route[%d]: expected pattern %s, got %s", i, pattern, routes[i].Pattern)
		}
		if routes[i].Handler == nil {
			t.Errorf("Route[%d]: handler is nil", i)
		}
	}
}

func TestAdminAPI_handlePolicies(t *testing.T) {
	cleanupSelectionPolicyRegistry()

	name := registerSelectionPolicy(SelectionPolicyInfo{
		ModuleID:           "http.reverse_proxy.selection_policies.dynamic",
		Key:                "{http.request.header.X-Tenant}",
		DataSourceType:     "etcd",
		DataSourceModuleID: "http.reverse_proxy.selection_policies.dynamic.sources.etcd",
		FallbackPolicy:     "http.reverse_proxy.selection_policies.random",
	})
	defer unregisterSelectionPolicy(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/policies", nil)
	w := httptest.NewRecorder()

	err := api.handlePolicies(w, req)
	if err != nil {
		t.Fatalf("handlePolicies returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response PoliciesResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response.Total != 1 {
		t.Fatalf("Expected total 1, got %d", response.Total)
	}
	if len(response.Policies) != 1 {
		t.Fatalf("Expected 1 policy, got %d", len(response.Policies))
	}
	if response.Policies[0].Name != name {
		t.Fatalf("Expected policy name %q, got %q", name, response.Policies[0].Name)
	}
	if response.Policies[0].Key == "" {
		t.Fatalf("Expected policy key to be set")
	}
}

func TestAdminAPI_handlePolicies_MethodNotAllowed(t *testing.T) {
	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodPost, "/dynamic-lb/policies", nil)
	w := httptest.NewRecorder()

	err := api.handlePolicies(w, req)
	if err == nil {
		t.Error("Expected error for POST method")
	}
}

func TestAdminAPI_handleStats(t *testing.T) {
	cleanupAdminRegistry()

	// Record some stats
	routeStatsRegistry.RecordHit("test-key", "")
	routeStatsRegistry.RecordMiss("test-key-2", "no_config")

	name := ds.RegisterAdminSource(&testAdminDS{cache: ds.AdminCacheStats{Hits: 3, Misses: 1}})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/stats", nil)
	w := httptest.NewRecorder()

	err := api.handleStats(w, req)
	if err != nil {
		t.Fatalf("handleStats returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.RouteHits == nil {
		t.Error("RouteHits should not be nil")
	}
	if response.CacheHits != 3 {
		t.Errorf("Expected CacheHits=3, got %d", response.CacheHits)
	}
}

func TestAdminAPI_handleStats_MethodNotAllowed(t *testing.T) {
	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodPost, "/dynamic-lb/stats", nil)
	w := httptest.NewRecorder()

	err := api.handleStats(w, req)
	if err == nil {
		t.Error("Expected error for POST method")
	}
}

func TestAdminAPI_handleHealth(t *testing.T) {
	cleanupAdminRegistry()

	sourceType := "test_admin_health"
	before := metrics.RouteConfigParseErrorSnapshot()[sourceType].Total
	metrics.RecordRouteConfigParseError(sourceType)
	metrics.RecordRouteConfigParseError(sourceType)

	name := ds.RegisterAdminSource(&testAdminDS{healthy: true})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/health", nil)
	w := httptest.NewRecorder()

	err := api.handleHealth(w, req)
	if err != nil {
		t.Fatalf("handleHealth returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Overall != "healthy" {
		t.Errorf("Expected overall 'healthy', got '%s'", response.Overall)
	}
	if _, ok := response.DataSources[name]; !ok {
		t.Fatalf("Expected health info for %q", name)
	}
	if response.ParseErrors == nil {
		t.Fatalf("Expected route_config_parse_errors to be present")
	}
	info, ok := response.ParseErrors[sourceType]
	if !ok {
		t.Fatalf("Expected parse error info for source_type %q", sourceType)
	}
	if info.Total != before+2 {
		t.Fatalf("Expected parse error total %d, got %d", before+2, info.Total)
	}
}

func TestAdminAPI_handleHealth_Degraded(t *testing.T) {
	cleanupAdminRegistry()

	name := ds.RegisterAdminSource(&testAdminDS{healthy: false, lastErr: "connection refused"})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/health", nil)
	w := httptest.NewRecorder()

	err := api.handleHealth(w, req)
	if err != nil {
		t.Fatalf("handleHealth returned error: %v", err)
	}

	var response HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Overall != "degraded" {
		t.Errorf("Expected overall 'degraded', got '%s'", response.Overall)
	}
	if response.DataSources[name].LastError != "connection refused" {
		t.Fatalf("Expected last_error 'connection refused', got %q", response.DataSources[name].LastError)
	}
}

func TestAdminAPI_handleRoutes(t *testing.T) {
	cleanupAdminRegistry()

	name := ds.RegisterAdminSource(&testAdminDS{routes: []ds.AdminRouteInfo{{Key: "k1", Upstream: "u1", RuleCount: 1}}})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/routes", nil)
	w := httptest.NewRecorder()

	err := api.handleRoutes(w, req)
	if err != nil {
		t.Fatalf("handleRoutes returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response RoutesResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response.Total != 1 {
		t.Fatalf("Expected total 1, got %d", response.Total)
	}
	if response.Routes[0].Source != name {
		t.Fatalf("Expected route source %q, got %q", name, response.Routes[0].Source)
	}
}

func TestAdminAPI_handleCache_Get(t *testing.T) {
	cleanupAdminRegistry()

	sourceType := "test_admin_cache"
	before := metrics.RouteConfigParseErrorSnapshot()[sourceType].Total
	metrics.RecordRouteConfigParseError(sourceType)

	name := ds.RegisterAdminSource(&testAdminDS{cache: ds.AdminCacheStats{Entries: 2, MaxSize: 10, Hits: 5, Misses: 5, NegativeHits: 1}})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodGet, "/dynamic-lb/cache", nil)
	w := httptest.NewRecorder()

	err := api.handleCache(w, req)
	if err != nil {
		t.Fatalf("handleCache GET returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response CacheResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response.Entries != 2 {
		t.Fatalf("Expected entries=2, got %d", response.Entries)
	}
	if response.Sources == nil || response.Sources[name].Entries != 2 {
		t.Fatalf("Expected per-source stats for %q", name)
	}
	if response.ParseErrors == nil {
		t.Fatalf("Expected route_config_parse_errors to be present")
	}
	info, ok := response.ParseErrors[sourceType]
	if !ok {
		t.Fatalf("Expected parse error info for source_type %q", sourceType)
	}
	if info.Total != before+1 {
		t.Fatalf("Expected parse error total %d, got %d", before+1, info.Total)
	}
}

func TestAdminAPI_handleCache_Delete(t *testing.T) {
	cleanupAdminRegistry()

	name := ds.RegisterAdminSource(&testAdminDS{cache: ds.AdminCacheStats{Entries: 1, Hits: 1}})
	defer ds.UnregisterAdminSource(name)

	api := AdminAPI{}
	req := httptest.NewRequest(http.MethodDelete, "/dynamic-lb/cache", nil)
	w := httptest.NewRecorder()

	err := api.handleCache(w, req)
	if err != nil {
		t.Fatalf("handleCache DELETE returned error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]any
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "cache cleared" {
		t.Errorf("Expected 'cache cleared', got '%v'", response["status"])
	}
	if response["cleared_sources"] == nil {
		t.Fatalf("Expected cleared_sources field")
	}
}

func TestRouteStatsRegistry(t *testing.T) {
	registry := NewRouteStatsRegistry()

	// Record some activity
	registry.RecordHit("key1", "")
	registry.RecordHit("key1", "")
	registry.RecordHit("key2", "")
	registry.RecordMiss("key3", "no_config")

	stats := registry.GetStats()

	if stats.Hits["key1"] != 2 {
		t.Errorf("Expected 2 hits for key1, got %d", stats.Hits["key1"])
	}

	if stats.Hits["key2"] != 1 {
		t.Errorf("Expected 1 hit for key2, got %d", stats.Hits["key2"])
	}

	if stats.Misses["key3"] != 1 {
		t.Errorf("Expected 1 miss for key3, got %d", stats.Misses["key3"])
	}
}
