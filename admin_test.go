package caddyslb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminAPI_Routes(t *testing.T) {
	api := AdminAPI{}
	routes := api.Routes()

	if len(routes) != 4 {
		t.Errorf("Expected 4 routes, got %d", len(routes))
	}

	expectedPatterns := []string{
		"/dynamic-lb/stats",
		"/dynamic-lb/health",
		"/dynamic-lb/routes",
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

func TestAdminAPI_handleStats(t *testing.T) {
	// Record some stats
	routeStatsRegistry.RecordHit("test-key")
	routeStatsRegistry.RecordMiss("test-key-2", "no_config")
	routeStatsRegistry.RecordCacheHit()
	routeStatsRegistry.RecordCacheMiss()

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
	// Register a data source
	dataSourceRegistry.Register("test-ds", nil, "etcd")
	dataSourceRegistry.UpdateHealth("test-ds", true, "")

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

	// Cleanup
	dataSourceRegistry.Unregister("test-ds")
}

func TestAdminAPI_handleHealth_Degraded(t *testing.T) {
	// Register an unhealthy data source
	dataSourceRegistry.Register("test-ds-unhealthy", nil, "redis")
	dataSourceRegistry.UpdateHealth("test-ds-unhealthy", false, "connection refused")

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

	// Cleanup
	dataSourceRegistry.Unregister("test-ds-unhealthy")
}

func TestAdminAPI_handleRoutes(t *testing.T) {
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
}

func TestAdminAPI_handleCache_Get(t *testing.T) {
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
}

func TestAdminAPI_handleCache_Delete(t *testing.T) {
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

	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "cache cleared" {
		t.Errorf("Expected 'cache cleared', got '%s'", response["status"])
	}
}

func TestRouteStatsRegistry(t *testing.T) {
	registry := NewRouteStatsRegistry()

	// Record some activity
	registry.RecordHit("key1")
	registry.RecordHit("key1")
	registry.RecordHit("key2")
	registry.RecordMiss("key3", "no_config")
	registry.RecordCacheHit()
	registry.RecordCacheHit()
	registry.RecordCacheMiss()

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

	if stats.CacheHits != 2 {
		t.Errorf("Expected 2 cache hits, got %d", stats.CacheHits)
	}

	if stats.CacheMisses != 1 {
		t.Errorf("Expected 1 cache miss, got %d", stats.CacheMisses)
	}

	expectedHitRate := 2.0 / 3.0
	if stats.CacheHitRate() < expectedHitRate-0.01 || stats.CacheHitRate() > expectedHitRate+0.01 {
		t.Errorf("Expected cache hit rate ~%f, got %f", expectedHitRate, stats.CacheHitRate())
	}
}

func TestDataSourceRegistry(t *testing.T) {
	registry := NewDataSourceRegistry()

	// Register
	registry.Register("ds1", nil, "etcd")
	registry.Register("ds2", nil, "redis")

	// Get health info
	healthInfo := registry.GetHealthInfo()
	if len(healthInfo) != 2 {
		t.Errorf("Expected 2 data sources, got %d", len(healthInfo))
	}

	if healthInfo["ds1"].Type != "etcd" {
		t.Errorf("Expected type 'etcd', got '%s'", healthInfo["ds1"].Type)
	}

	// Update health
	registry.UpdateHealth("ds1", false, "connection refused")
	healthInfo = registry.GetHealthInfo()

	if healthInfo["ds1"].Healthy {
		t.Error("Expected ds1 to be unhealthy")
	}

	if healthInfo["ds1"].LastError != "connection refused" {
		t.Errorf("Expected 'connection refused', got '%s'", healthInfo["ds1"].LastError)
	}

	// Unregister
	registry.Unregister("ds1")
	healthInfo = registry.GetHealthInfo()

	if len(healthInfo) != 1 {
		t.Errorf("Expected 1 data source after unregister, got %d", len(healthInfo))
	}
}

func TestRouteStats_CacheHitRate_ZeroTotal(t *testing.T) {
	stats := RouteStats{
		Hits:        make(map[string]int64),
		Misses:      make(map[string]int64),
		CacheHits:   0,
		CacheMisses: 0,
	}

	if stats.CacheHitRate() != 0.0 {
		t.Errorf("Expected 0.0 for zero total, got %f", stats.CacheHitRate())
	}
}
