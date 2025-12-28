package caddyslb

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/extractor"
	"github.com/puxu-msft/caddy-dynamic-routing/matcher"
)

// mockDataSource is a simple mock implementation for testing
type mockDataSource struct {
	configs map[string]*datasource.RouteConfig
	healthy bool
}

func (m *mockDataSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "test.mock"}
}

func (m *mockDataSource) Provision(ctx caddy.Context) error {
	return nil
}

func (m *mockDataSource) Cleanup() error {
	return nil
}

func (m *mockDataSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	if config, ok := m.configs[key]; ok {
		return config, nil
	}
	return nil, nil
}

func (m *mockDataSource) Healthy() bool {
	return m.healthy
}

func TestDynamicSelection_CaddyModule(t *testing.T) {
	s := &DynamicSelection{}
	info := s.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*DynamicSelection); !ok {
		t.Error("New() should return *DynamicSelection")
	}
}

func TestDynamicSelection_Select_EmptyPool(t *testing.T) {
	s := &DynamicSelection{
		logger:   zap.NewNop(),
		fallback: new(reverseproxy.RandomSelection),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	result := s.Select(nil, req, nil)
	if result != nil {
		t.Error("Expected nil for empty pool")
	}

	result = s.Select(reverseproxy.UpstreamPool{}, req, nil)
	if result != nil {
		t.Error("Expected nil for empty pool")
	}
}

func TestDynamicSelection_Select_NoReplacer(t *testing.T) {
	s := &DynamicSelection{
		logger:   zap.NewNop(),
		fallback: new(reverseproxy.RandomSelection),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	// Request context without replacer
	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_NoKeyExtractorOrDataSource(t *testing.T) {
	s := &DynamicSelection{
		logger:      zap.NewNop(),
		fallback:    new(reverseproxy.RandomSelection),
		ruleMatcher: matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	// Add replacer to context
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, caddy.NewReplacer())
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_EmptyKey(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {Upstream: "backend-a:8080"},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	// No X-Tenant header - key will be empty
	repl := caddy.NewReplacer()
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_UnhealthyDataSource(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {Upstream: "backend-a:8080"},
		},
		healthy: false, // Unhealthy
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_ConfigNotFound(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-xyz")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_DisabledConfig(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	disabled := false
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {Upstream: "backend-a:8080", Enabled: &disabled},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_ExpiredConfig(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")

	// Mark config as expired via TTL and UpdatedAt.
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {
				Upstream:  "backend-a:8080",
				TTL:       10 * time.Millisecond,
				UpdatedAt: time.Now().Add(-time.Second),
			},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_Success(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {Upstream: "backend-a:8080"},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.Dial != "backend-a:8080" {
		t.Errorf("Expected 'backend-a:8080', got '%s'", result.Dial)
	}
}

func TestDynamicSelection_Select_UpstreamNotInPool(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")
	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {Upstream: "backend-missing:8080"},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:       zap.NewNop(),
		fallback:     new(reverseproxy.RandomSelection),
		keyExtractor: keyExt,
		dataSource:   mock,
		ruleMatcher:  matcher.NewRuleMatcher(),
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	// Should fallback since matched upstream is not in pool
	if result == nil {
		t.Error("Expected fallback to return an upstream")
	}
}

func TestDynamicSelection_Select_InstancePrefer_FallbackToVersion_WhenInstanceUnavailable(t *testing.T) {
	instanceExt, _ := extractor.NewFromExpression("{http.vars.route_instance_key}")
	versionExt, _ := extractor.NewFromExpression("{http.vars.route_version_key}")

	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"t/s/v/i": {Upstream: "backend-i:8080"},
			"t/s/v":   {Upstream: "backend-v:8080"},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:               zap.NewNop(),
		fallback:             new(reverseproxy.RandomSelection),
		instanceKeyExtractor: instanceExt,
		versionKeyExtractor:  versionExt,
		dataSource:           mock,
		ruleMatcher:          matcher.NewRuleMatcher(),
		isUpstreamAvailable:  func(u *reverseproxy.Upstream) bool { return u.Dial != "backend-i:8080" },
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-i:8080"},
		{Dial: "backend-v:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.vars.route_instance_key", "t/s/v/i")
	repl.Set("http.vars.route_version_key", "t/s/v")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.Dial != "backend-v:8080" {
		t.Fatalf("Expected fallback to version upstream 'backend-v:8080', got %q", result.Dial)
	}
}

func TestDynamicSelection_Select_FiltersUnavailableCandidates(t *testing.T) {
	keyExt, _ := extractor.NewFromExpression("{header.X-Tenant}")

	mock := &mockDataSource{
		configs: map[string]*datasource.RouteConfig{
			"tenant-a": {
				Algorithm: string(matcher.AlgorithmFirst),
				Rules: []datasource.Rule{
					{
						Match: map[string]string{},
						Upstreams: []datasource.WeightedUpstream{
							{Address: "backend-a:8080", Weight: 1},
							{Address: "backend-b:8080", Weight: 1},
						},
						Priority: 0,
					},
				},
			},
		},
		healthy: true,
	}

	s := &DynamicSelection{
		logger:              zap.NewNop(),
		fallback:            new(reverseproxy.RandomSelection),
		keyExtractor:        keyExt,
		dataSource:          mock,
		ruleMatcher:         matcher.NewRuleMatcher(),
		isUpstreamAvailable: func(u *reverseproxy.Upstream) bool { return u.Dial != "backend-a:8080" },
	}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()
	repl.Set("http.request.header.X-Tenant", "tenant-a")
	ctx := context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl)
	req = req.WithContext(ctx)

	result := s.Select(pool, req, nil)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.Dial != "backend-b:8080" {
		t.Fatalf("Expected unavailable candidate to be filtered and select 'backend-b:8080', got %q", result.Dial)
	}
}

func TestDynamicSelection_FindUpstreamInPool(t *testing.T) {
	s := &DynamicSelection{}

	pool := reverseproxy.UpstreamPool{
		{Dial: "backend-a:8080"},
		{Dial: "backend-b:8080"},
		{Dial: "backend-c:8080"},
	}

	t.Run("found", func(t *testing.T) {
		result := s.findUpstreamInPool(pool, "backend-b:8080")
		if result == nil {
			t.Fatal("Expected to find upstream")
		}
		if result.Dial != "backend-b:8080" {
			t.Errorf("Expected 'backend-b:8080', got '%s'", result.Dial)
		}
	})

	t.Run("not found", func(t *testing.T) {
		result := s.findUpstreamInPool(pool, "backend-z:8080")
		if result != nil {
			t.Error("Expected nil for not found")
		}
	})

	t.Run("empty pool", func(t *testing.T) {
		result := s.findUpstreamInPool(nil, "backend-a:8080")
		if result != nil {
			t.Error("Expected nil for empty pool")
		}
	})
}

func TestDynamicSelection_Cleanup(t *testing.T) {
	s := &DynamicSelection{}
	err := s.Cleanup()
	if err != nil {
		t.Errorf("Cleanup should not return error, got: %v", err)
	}
}

func TestDynamicSelection_InterfaceGuards(t *testing.T) {
	// These are compile-time checks, but we can verify they exist
	var _ caddy.Module = (*DynamicSelection)(nil)
	var _ caddy.Provisioner = (*DynamicSelection)(nil)
	var _ caddy.CleanerUpper = (*DynamicSelection)(nil)
	var _ reverseproxy.Selector = (*DynamicSelection)(nil)
}
