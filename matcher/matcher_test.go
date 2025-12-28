package matcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

func TestRuleMatcher_Match_SimpleMode(t *testing.T) {
	m := NewRuleMatcher()

	config := &datasource.RouteConfig{
		Upstream: "backend:8080",
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()

	result := m.Match(req, repl, config)
	if result == nil {
		t.Fatal("Expected non-nil result for simple mode")
	}
	if result.Address != "backend:8080" {
		t.Errorf("Expected address 'backend:8080', got '%s'", result.Address)
	}
	if result.Weight != 1 {
		t.Errorf("Expected weight 1, got %d", result.Weight)
	}
}

func TestRuleMatcher_MatchResult_SimpleMode(t *testing.T) {
	m := NewRuleMatcher()

	config := &datasource.RouteConfig{
		Upstream:     "backend:8080",
		Algorithm:    string(AlgorithmRoundRobin),
		AlgorithmKey: "",
	}

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()

	res := m.MatchResult(req, repl, config)
	if res == nil {
		t.Fatal("Expected non-nil MatchResult")
	}
	if !res.MatchedSimple {
		t.Fatalf("Expected MatchedSimple=true")
	}
	if res.Algorithm != string(AlgorithmRoundRobin) {
		t.Fatalf("Expected algorithm %q, got %q", AlgorithmRoundRobin, res.Algorithm)
	}
	if len(res.Upstreams) != 1 || res.Upstreams[0].Address != "backend:8080" {
		t.Fatalf("Expected single candidate backend:8080, got %#v", res.Upstreams)
	}
}

func TestRuleMatcher_Match_NilConfig(t *testing.T) {
	m := NewRuleMatcher()

	req := httptest.NewRequest("GET", "/test", nil)
	repl := caddy.NewReplacer()

	result := m.Match(req, repl, nil)
	if result != nil {
		t.Error("Expected nil result for nil config")
	}
}

func TestRuleMatcher_Match_RulesMode(t *testing.T) {
	m := NewRuleMatcher()

	config := &datasource.RouteConfig{
		Rules: []datasource.Rule{
			{
				Match: map[string]string{
					"http.request.header.X-Version": "v2",
				},
				Upstreams: []datasource.WeightedUpstream{
					{Address: "backend-v2:8080", Weight: 1},
				},
				Priority: 10,
			},
			{
				Match: map[string]string{}, // Default rule
				Upstreams: []datasource.WeightedUpstream{
					{Address: "backend-default:8080", Weight: 1},
				},
				Priority: 0,
			},
		},
	}

	t.Run("match first rule", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		repl := caddy.NewReplacer()
		repl.Set("http.request.header.X-Version", "v2")

		result := m.Match(req, repl, config)
		if result == nil {
			t.Fatal("Expected non-nil result")
		}
		if result.Address != "backend-v2:8080" {
			t.Errorf("Expected 'backend-v2:8080', got '%s'", result.Address)
		}
	})

	t.Run("fall through to default rule", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		repl := caddy.NewReplacer()
		repl.Set("http.request.header.X-Version", "v1")

		result := m.Match(req, repl, config)
		if result == nil {
			t.Fatal("Expected non-nil result")
		}
		if result.Address != "backend-default:8080" {
			t.Errorf("Expected 'backend-default:8080', got '%s'", result.Address)
		}
	})
}

func TestRuleMatcher_MatchConditions(t *testing.T) {
	m := NewRuleMatcher()

	tests := []struct {
		name       string
		conditions map[string]string
		replValues map[string]string
		expected   bool
	}{
		{
			name:       "empty conditions always match",
			conditions: map[string]string{},
			replValues: map[string]string{},
			expected:   true,
		},
		{
			name:       "exact match success",
			conditions: map[string]string{"http.request.header.X-Version": "v2"},
			replValues: map[string]string{"http.request.header.X-Version": "v2"},
			expected:   true,
		},
		{
			name:       "exact match failure",
			conditions: map[string]string{"http.request.header.X-Version": "v2"},
			replValues: map[string]string{"http.request.header.X-Version": "v1"},
			expected:   false,
		},
		{
			name:       "multiple conditions all match",
			conditions: map[string]string{"http.request.header.X-Version": "v2", "http.request.header.X-Env": "prod"},
			replValues: map[string]string{"http.request.header.X-Version": "v2", "http.request.header.X-Env": "prod"},
			expected:   true,
		},
		{
			name:       "multiple conditions one fails",
			conditions: map[string]string{"http.request.header.X-Version": "v2", "http.request.header.X-Env": "prod"},
			replValues: map[string]string{"http.request.header.X-Version": "v2", "http.request.header.X-Env": "staging"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repl := caddy.NewReplacer()
			for k, v := range tt.replValues {
				repl.Set(k, v)
			}

			result := m.matchConditions(nil, repl, tt.conditions)
			if result != tt.expected {
				t.Errorf("matchConditions() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRuleMatcher_MatchValue_Regex(t *testing.T) {
	m := NewRuleMatcher()

	tests := []struct {
		name     string
		actual   string
		expected string
		match    bool
	}{
		{"regex match", "v2.1.0", "~v2\\.", true},
		{"regex no match", "v1.0.0", "~v2\\.", false},
		{"regex full match", "hello-world", "~^hello-.*$", true},
		{"regex invalid pattern", "test", "~[invalid", false},
		{"exact match", "v2", "v2", true},
		{"exact no match", "v2", "v3", false},
		{"empty expected matches empty actual", "", "", true},
		{"empty expected does not match non-empty", "value", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.matchValue(tt.actual, tt.expected)
			if result != tt.match {
				t.Errorf("matchValue(%q, %q) = %v, want %v", tt.actual, tt.expected, result, tt.match)
			}
		})
	}
}

func TestRuleMatcher_RegexCache(t *testing.T) {
	m := NewRuleMatcher()

	pattern := "v2\\.[0-9]+"

	// First call - compiles and caches
	re1, err := m.getRegex(pattern)
	if err != nil {
		t.Fatalf("getRegex failed: %v", err)
	}

	// Second call - should return cached
	re2, err := m.getRegex(pattern)
	if err != nil {
		t.Fatalf("getRegex failed: %v", err)
	}

	// Should be the same instance
	if re1 != re2 {
		t.Error("Expected cached regex to be returned")
	}
}

func TestRuleMatcher_CachesAreBounded(t *testing.T) {
	m := NewRuleMatcherWithCacheSizes(2, 2)

	// Populate regex cache beyond capacity.
	for _, pattern := range []string{"a", "b", "c", "d"} {
		if _, err := m.getRegex(pattern); err != nil {
			t.Fatalf("getRegex(%q) returned error: %v", pattern, err)
		}
	}
	if m.regexCache == nil {
		t.Fatalf("expected regexCache to be initialized")
	}
	if got := m.regexCache.Len(); got > 2 {
		t.Fatalf("expected regexCache Len <= 2, got %d", got)
	}

	// Populate selector cache beyond capacity.
	_ = m.getSelector(string(AlgorithmWeightedRandom), "k1")
	_ = m.getSelector(string(AlgorithmWeightedRandom), "k2")
	_ = m.getSelector(string(AlgorithmWeightedRandom), "k3")
	_ = m.getSelector(string(AlgorithmWeightedRandom), "k4")
	if m.selectorCache == nil {
		t.Fatalf("expected selectorCache to be initialized")
	}
	if got := m.selectorCache.Len(); got > 2 {
		t.Fatalf("expected selectorCache Len <= 2, got %d", got)
	}
}

func TestRuleMatcher_SelectUpstream(t *testing.T) {
	m := NewRuleMatcher()
	req := httptest.NewRequest("GET", "/test", nil)

	t.Run("empty upstreams", func(t *testing.T) {
		result := m.selectUpstream(req, nil, string(AlgorithmWeightedRandom), "")
		if result != nil {
			t.Error("Expected nil for empty upstreams")
		}
	})

	t.Run("single upstream", func(t *testing.T) {
		upstreams := []datasource.WeightedUpstream{
			{Address: "backend:8080", Weight: 1},
		}
		result := m.selectUpstream(req, upstreams, string(AlgorithmWeightedRandom), "")
		if result == nil {
			t.Fatal("Expected non-nil result")
		}
		if result.Address != "backend:8080" {
			t.Errorf("Expected 'backend:8080', got '%s'", result.Address)
		}
	})

	t.Run("weighted selection distribution", func(t *testing.T) {
		upstreams := []datasource.WeightedUpstream{
			{Address: "backend-a:8080", Weight: 80},
			{Address: "backend-b:8080", Weight: 20},
		}

		counts := make(map[string]int)
		iterations := 10000

		for i := 0; i < iterations; i++ {
			result := m.selectUpstream(req, upstreams, string(AlgorithmWeightedRandom), "")
			counts[result.Address]++
		}

		// With 80/20 weight, expect roughly 80% to backend-a
		ratioA := float64(counts["backend-a:8080"]) / float64(iterations)
		if ratioA < 0.7 || ratioA > 0.9 {
			t.Errorf("Expected backend-a ratio ~0.8, got %f", ratioA)
		}
	})
}

func TestRuleMatcher_ResolvePlaceholder(t *testing.T) {
	m := NewRuleMatcher()

	t.Run("nil replacer", func(t *testing.T) {
		result := m.resolvePlaceholder(nil, nil, "http.request.header.X-Test")
		if result != "" {
			t.Errorf("Expected empty for nil replacer, got %q", result)
		}
	})

	t.Run("request fallback when replacer is nil", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/path", nil)
		if err != nil {
			t.Fatalf("http.NewRequestWithContext: %v", err)
		}
		req.Header.Set("X-Test", "value")
		result := m.resolvePlaceholder(req, nil, "{http.request.header.X-Test}")
		if result != "value" {
			t.Errorf("Expected 'value', got %q", result)
		}
	})

	t.Run("with curly braces", func(t *testing.T) {
		repl := caddy.NewReplacer()
		repl.Set("http.request.header.X-Test", "value")
		result := m.resolvePlaceholder(nil, repl, "{http.request.header.X-Test}")
		if result != "value" {
			t.Errorf("Expected 'value', got %q", result)
		}
	})

	t.Run("without curly braces", func(t *testing.T) {
		repl := caddy.NewReplacer()
		repl.Set("http.request.header.X-Test", "value")
		result := m.resolvePlaceholder(nil, repl, "http.request.header.X-Test")
		if result != "value" {
			t.Errorf("Expected 'value', got %q", result)
		}
	})
}

// TestRuleMatcher_MatchConditions_MixedExactAndRegex tests the optimization
// that processes exact matches before regex patterns for fast-fail.
func TestRuleMatcher_MatchConditions_MixedExactAndRegex(t *testing.T) {
	m := NewRuleMatcher()

	tests := []struct {
		name       string
		conditions map[string]string
		replValues map[string]string
		expected   bool
	}{
		{
			name: "exact match passes, regex passes",
			conditions: map[string]string{
				"http.request.header.X-Tenant":  "acme",
				"http.request.header.X-Version": "~v2\\.[0-9]+",
			},
			replValues: map[string]string{
				"http.request.header.X-Tenant":  "acme",
				"http.request.header.X-Version": "v2.5",
			},
			expected: true,
		},
		{
			name: "exact match fails, regex would pass (fast-fail)",
			conditions: map[string]string{
				"http.request.header.X-Tenant":  "acme",
				"http.request.header.X-Version": "~v2\\.[0-9]+",
			},
			replValues: map[string]string{
				"http.request.header.X-Tenant":  "other",
				"http.request.header.X-Version": "v2.5",
			},
			expected: false,
		},
		{
			name: "exact match passes, regex fails",
			conditions: map[string]string{
				"http.request.header.X-Tenant":  "acme",
				"http.request.header.X-Version": "~v2\\.[0-9]+",
			},
			replValues: map[string]string{
				"http.request.header.X-Tenant":  "acme",
				"http.request.header.X-Version": "v1.0",
			},
			expected: false,
		},
		{
			name: "multiple exact matches, one regex",
			conditions: map[string]string{
				"http.request.header.X-Tenant": "acme",
				"http.request.header.X-Env":    "prod",
				"http.request.header.X-Path":   "~^/api/.*",
			},
			replValues: map[string]string{
				"http.request.header.X-Tenant": "acme",
				"http.request.header.X-Env":    "prod",
				"http.request.header.X-Path":   "/api/users",
			},
			expected: true,
		},
		{
			name: "all regex conditions",
			conditions: map[string]string{
				"http.request.header.X-Version": "~v[0-9]+",
				"http.request.header.X-Path":    "~^/api/.*",
			},
			replValues: map[string]string{
				"http.request.header.X-Version": "v2",
				"http.request.header.X-Path":    "/api/users",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repl := caddy.NewReplacer()
			for k, v := range tt.replValues {
				repl.Set(k, v)
			}

			result := m.matchConditions(nil, repl, tt.conditions)
			if result != tt.expected {
				t.Errorf("matchConditions() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestRuleMatcher_SelectorCacheKey tests that the selector cache works correctly.
func TestRuleMatcher_SelectorCacheKey(t *testing.T) {
	m := NewRuleMatcher()

	// Get selector with same algorithm and key should return cached instance
	s1 := m.getSelector("round_robin", "")
	s2 := m.getSelector("round_robin", "")
	if s1 != s2 {
		t.Error("Expected same selector instance for same algorithm and key")
	}

	// Different algorithm should return different selector
	s3 := m.getSelector("weighted_random", "")
	if s1 == s3 {
		t.Error("Expected different selector for different algorithm")
	}

	// Same algorithm but different key should return different selector
	s4 := m.getSelector("header_hash", "X-Tenant")
	s5 := m.getSelector("header_hash", "X-Region")
	if s4 == s5 {
		t.Error("Expected different selector for different key")
	}

	// Same algorithm and key should return cached instance
	s6 := m.getSelector("header_hash", "X-Tenant")
	if s4 != s6 {
		t.Error("Expected same selector for same algorithm and key")
	}
}
