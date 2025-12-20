package composite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

// mockDataSource is a test implementation of DataSource.
type mockDataSource struct {
	config    *datasource.RouteConfig
	err       error
	healthy   bool
	getCalled int
}

func (m *mockDataSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "test.mock"}
}

func (m *mockDataSource) Provision(ctx caddy.Context) error { return nil }
func (m *mockDataSource) Cleanup() error                    { return nil }

func (m *mockDataSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	m.getCalled++
	return m.config, m.err
}

func (m *mockDataSource) Healthy() bool {
	return m.healthy
}

func TestCompositeSource_CaddyModule(t *testing.T) {
	c := CompositeSource{}
	info := c.CaddyModule()
	if info.ID != "http.reverse_proxy.selection_policies.dynamic.sources.composite" {
		t.Errorf("unexpected module ID: %s", info.ID)
	}
}

func TestCompositeSource_Failover_FirstHealthy(t *testing.T) {
	src1 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend1:8080"},
		healthy: true,
	}
	src2 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend2:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModeFailover,
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if config.Upstream != "backend1:8080" {
		t.Errorf("expected backend1:8080, got %s", config.Upstream)
	}
	if src1.getCalled != 1 {
		t.Errorf("expected src1 to be called once, got %d", src1.getCalled)
	}
	if src2.getCalled != 0 {
		t.Errorf("expected src2 to not be called, got %d", src2.getCalled)
	}
}

func TestCompositeSource_Failover_SkipsUnhealthy(t *testing.T) {
	src1 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend1:8080"},
		healthy: false, // unhealthy
	}
	src2 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend2:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModeFailover,
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if config.Upstream != "backend2:8080" {
		t.Errorf("expected backend2:8080, got %s", config.Upstream)
	}
	if src1.getCalled != 0 {
		t.Errorf("expected src1 to not be called (unhealthy), got %d", src1.getCalled)
	}
}

func TestCompositeSource_Failover_SkipsOnError(t *testing.T) {
	src1 := &mockDataSource{
		err:     errors.New("connection error"),
		healthy: true,
	}
	src2 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend2:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModeFailover,
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if config.Upstream != "backend2:8080" {
		t.Errorf("expected backend2:8080, got %s", config.Upstream)
	}
}

func TestCompositeSource_Merge_Sequential(t *testing.T) {
	src1 := &mockDataSource{
		config: &datasource.RouteConfig{
			Upstream:  "backend1:8080",
			Algorithm: "round_robin",
			Metadata:  map[string]string{"env": "prod"},
		},
		healthy: true,
	}
	src2 := &mockDataSource{
		config: &datasource.RouteConfig{
			Upstream: "backend2:8080", // overrides
			Metadata: map[string]string{"region": "us-east"},
		},
		healthy: true,
	}

	c := &CompositeSource{
		sources:  []datasource.DataSource{src1, src2},
		Mode:     ModeMerge,
		Parallel: false,
		Timeout:  caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	// src2 upstream overrides src1
	if config.Upstream != "backend2:8080" {
		t.Errorf("expected backend2:8080, got %s", config.Upstream)
	}
	// Algorithm from src1 preserved
	if config.Algorithm != "round_robin" {
		t.Errorf("expected round_robin, got %s", config.Algorithm)
	}
	// Metadata merged
	if config.Metadata["env"] != "prod" {
		t.Errorf("expected env=prod, got %s", config.Metadata["env"])
	}
	if config.Metadata["region"] != "us-east" {
		t.Errorf("expected region=us-east, got %s", config.Metadata["region"])
	}
}

func TestCompositeSource_Merge_Parallel(t *testing.T) {
	src1 := &mockDataSource{
		config: &datasource.RouteConfig{
			Upstream:  "backend1:8080",
			Algorithm: "round_robin",
		},
		healthy: true,
	}
	src2 := &mockDataSource{
		config: &datasource.RouteConfig{
			Fallback: "random",
		},
		healthy: true,
	}

	c := &CompositeSource{
		sources:  []datasource.DataSource{src1, src2},
		Mode:     ModeMerge,
		Parallel: true,
		Timeout:  caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if config.Upstream != "backend1:8080" {
		t.Errorf("expected backend1:8080, got %s", config.Upstream)
	}
	if config.Fallback != "random" {
		t.Errorf("expected random, got %s", config.Fallback)
	}
}

func TestCompositeSource_Merge_Rules(t *testing.T) {
	src1 := &mockDataSource{
		config: &datasource.RouteConfig{
			Rules: []datasource.Rule{
				{Priority: 10, Upstreams: []datasource.WeightedUpstream{{Address: "a:8080"}}},
			},
		},
		healthy: true,
	}
	src2 := &mockDataSource{
		config: &datasource.RouteConfig{
			Rules: []datasource.Rule{
				{Priority: 5, Upstreams: []datasource.WeightedUpstream{{Address: "b:8080"}}},
			},
		},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModeMerge,
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if len(config.Rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(config.Rules))
	}
}

func TestCompositeSource_Priority(t *testing.T) {
	src1 := &mockDataSource{
		config:  nil, // no config
		healthy: false,
	}
	src2 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend2:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModePriority, // doesn't check health
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	// src1 is called even though unhealthy (priority mode)
	if src1.getCalled != 1 {
		t.Errorf("expected src1 to be called, got %d", src1.getCalled)
	}
}

func TestCompositeSource_First(t *testing.T) {
	src1 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend1:8080"},
		healthy: true,
	}
	src2 := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend2:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources: []datasource.DataSource{src1, src2},
		Mode:    ModeFirst,
		Timeout: caddy.Duration(5 * time.Second),
	}

	config, err := c.Get(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config, got nil")
	}
	if config.Upstream != "backend1:8080" {
		t.Errorf("expected backend1:8080, got %s", config.Upstream)
	}
	if src1.getCalled != 1 {
		t.Errorf("expected src1 to be called once, got %d", src1.getCalled)
	}
	if src2.getCalled != 0 {
		t.Errorf("expected src2 to not be called, got %d", src2.getCalled)
	}
}

func TestCompositeSource_Caching(t *testing.T) {
	src := &mockDataSource{
		config:  &datasource.RouteConfig{Upstream: "backend:8080"},
		healthy: true,
	}

	c := &CompositeSource{
		sources:      []datasource.DataSource{src},
		Mode:         ModeFirst,
		Timeout:      caddy.Duration(5 * time.Second),
		CacheResults: true,
		CacheTTL:     caddy.Duration(1 * time.Second),
	}

	// First call
	config, _ := c.Get(context.Background(), "test-key")
	if config == nil {
		t.Fatal("expected config")
	}
	if src.getCalled != 1 {
		t.Errorf("expected 1 call, got %d", src.getCalled)
	}

	// Second call - should use cache
	config, _ = c.Get(context.Background(), "test-key")
	if config == nil {
		t.Fatal("expected config from cache")
	}
	if src.getCalled != 1 {
		t.Errorf("expected still 1 call (cached), got %d", src.getCalled)
	}

	// Wait for cache to expire
	time.Sleep(1100 * time.Millisecond)

	// Third call - cache expired
	config, _ = c.Get(context.Background(), "test-key")
	if config == nil {
		t.Fatal("expected config after cache expiry")
	}
	if src.getCalled != 2 {
		t.Errorf("expected 2 calls after cache expiry, got %d", src.getCalled)
	}
}

func TestCompositeSource_Healthy(t *testing.T) {
	tests := []struct {
		name     string
		sources  []datasource.DataSource
		expected bool
	}{
		{
			name: "all healthy",
			sources: []datasource.DataSource{
				&mockDataSource{healthy: true},
				&mockDataSource{healthy: true},
			},
			expected: true,
		},
		{
			name: "one healthy",
			sources: []datasource.DataSource{
				&mockDataSource{healthy: false},
				&mockDataSource{healthy: true},
			},
			expected: true,
		},
		{
			name: "none healthy",
			sources: []datasource.DataSource{
				&mockDataSource{healthy: false},
				&mockDataSource{healthy: false},
			},
			expected: false,
		},
		{
			name:     "no sources",
			sources:  []datasource.DataSource{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CompositeSource{sources: tt.sources}
			if got := c.Healthy(); got != tt.expected {
				t.Errorf("Healthy() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCompositeSource_HealthStatus(t *testing.T) {
	c := &CompositeSource{
		sources: []datasource.DataSource{
			&mockDataSource{healthy: true},
			&mockDataSource{healthy: false},
			&mockDataSource{healthy: true},
		},
	}

	status := c.HealthStatus()
	if len(status) != 3 {
		t.Errorf("expected 3 entries, got %d", len(status))
	}
	if !status[0] {
		t.Error("expected source 0 to be healthy")
	}
	if status[1] {
		t.Error("expected source 1 to be unhealthy")
	}
	if !status[2] {
		t.Error("expected source 2 to be healthy")
	}
}

func TestMergeConfig_Version(t *testing.T) {
	c := &CompositeSource{}

	dst := &datasource.RouteConfig{Version: 1}
	src := &datasource.RouteConfig{Version: 5}

	c.mergeConfig(dst, src)

	if dst.Version != 5 {
		t.Errorf("expected version 5, got %d", dst.Version)
	}
}

func TestMergeConfig_Enabled(t *testing.T) {
	c := &CompositeSource{}

	enabled := true
	disabled := false

	dst := &datasource.RouteConfig{Enabled: &enabled}
	src := &datasource.RouteConfig{Enabled: &disabled}

	c.mergeConfig(dst, src)

	if dst.Enabled == nil || *dst.Enabled != false {
		t.Error("expected enabled to be false")
	}
}
