package datasource

import (
	"testing"
	"time"
)

func TestParseRouteConfig_SimpleFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain address", "backend:8080", "backend:8080"},
		{"with hostname", "backend.example.com:8080", "backend.example.com:8080"},
		{"ip address", "192.168.1.100:8080", "192.168.1.100:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := ParseRouteConfig([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseRouteConfig failed: %v", err)
			}
			if config == nil {
				t.Fatal("Expected config, got nil")
			}
			if config.Upstream != tt.expected {
				t.Errorf("Expected upstream '%s', got '%s'", tt.expected, config.Upstream)
			}
		})
	}
}

func TestParseRouteConfig_JSONString(t *testing.T) {
	// JSON encoded string
	input := `"backend:8080"`
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	if config.Upstream != "backend:8080" {
		t.Errorf("Expected upstream 'backend:8080', got '%s'", config.Upstream)
	}
}

func TestParseRouteConfig_JSONObject(t *testing.T) {
	input := `{
		"upstream": "backend:8080",
		"fallback": "random"
	}`
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	if config.Upstream != "backend:8080" {
		t.Errorf("Expected upstream 'backend:8080', got '%s'", config.Upstream)
	}
	if config.Fallback != "random" {
		t.Errorf("Expected fallback 'random', got '%s'", config.Fallback)
	}
}

func TestParseRouteConfig_WithRules(t *testing.T) {
	input := `{
		"rules": [
			{
				"match": {"http.request.header.X-Version": "v2"},
				"upstreams": [
					{"address": "backend-v2:8080", "weight": 80},
					{"address": "backend-v2-backup:8080", "weight": 20}
				],
				"priority": 10
			},
			{
				"match": {},
				"upstreams": [{"address": "backend-default:8080"}],
				"priority": 0
			}
		],
		"fallback": "round_robin"
	}`
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	if len(config.Rules) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(config.Rules))
	}
	// Rules should be sorted by priority (highest first)
	if config.Rules[0].Priority != 10 {
		t.Errorf("Expected first rule priority 10, got %d", config.Rules[0].Priority)
	}
	if config.Rules[1].Priority != 0 {
		t.Errorf("Expected second rule priority 0, got %d", config.Rules[1].Priority)
	}
	// Check weights
	if config.Rules[0].Upstreams[0].Weight != 80 {
		t.Errorf("Expected weight 80, got %d", config.Rules[0].Upstreams[0].Weight)
	}
	// Default weight should be 1
	if config.Rules[1].Upstreams[0].Weight != 1 {
		t.Errorf("Expected default weight 1, got %d", config.Rules[1].Upstreams[0].Weight)
	}
}

func TestParseRouteConfig_EmptyInput(t *testing.T) {
	config, err := ParseRouteConfig([]byte{})
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config != nil {
		t.Error("Expected nil config for empty input")
	}
}

func TestParseRouteConfig_Whitespace(t *testing.T) {
	input := "  \n\t  backend:8080"
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	// After trimming whitespace, should get the address
	if config.Upstream != "backend:8080" {
		t.Errorf("Expected upstream 'backend:8080', got '%s'", config.Upstream)
	}
}

func TestParseRouteConfig_YAML(t *testing.T) {
	input := `
upstream: backend:8080
fallback: random
algorithm: round_robin
`
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	if config.Upstream != "backend:8080" {
		t.Errorf("Expected upstream 'backend:8080', got '%s'", config.Upstream)
	}
	if config.Fallback != "random" {
		t.Errorf("Expected fallback 'random', got '%s'", config.Fallback)
	}
	if config.Algorithm != "round_robin" {
		t.Errorf("Expected algorithm 'round_robin', got '%s'", config.Algorithm)
	}
}

func TestParseRouteConfig_YAMLWithRules(t *testing.T) {
	input := `
rules:
  - match:
      http.request.header.X-Version: v2
    upstreams:
      - address: backend-v2:8080
        weight: 80
      - address: backend-v2-backup:8080
        weight: 20
    priority: 10
  - upstreams:
      - address: backend-default:8080
    priority: 0
fallback: round_robin
`
	config, err := ParseRouteConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteConfig failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config, got nil")
	}
	if len(config.Rules) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(config.Rules))
	}
	// Rules should be sorted by priority (highest first)
	if config.Rules[0].Priority != 10 {
		t.Errorf("Expected first rule priority 10, got %d", config.Rules[0].Priority)
	}
	if config.Rules[1].Priority != 0 {
		t.Errorf("Expected second rule priority 0, got %d", config.Rules[1].Priority)
	}
}

func TestParseRouteConfigWithFormat_JSON(t *testing.T) {
	input := `{"upstream": "backend:8080", "fallback": "random"}`
	config, err := ParseRouteConfigWithFormat([]byte(input), "json")
	if err != nil {
		t.Fatalf("ParseRouteConfigWithFormat failed: %v", err)
	}
	if config.Upstream != "backend:8080" {
		t.Errorf("Expected upstream 'backend:8080', got '%s'", config.Upstream)
	}
}

func TestParseRouteConfigWithFormat_YAML(t *testing.T) {
	input := `upstream: backend:9090`
	config, err := ParseRouteConfigWithFormat([]byte(input), "yaml")
	if err != nil {
		t.Fatalf("ParseRouteConfigWithFormat failed: %v", err)
	}
	if config.Upstream != "backend:9090" {
		t.Errorf("Expected upstream 'backend:9090', got '%s'", config.Upstream)
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"JSON object", `{"upstream": "a:8080"}`, "json"},
		{"JSON string", `"a:8080"`, "json"},
		{"JSON array", `[{"address": "a:8080"}]`, "json"},
		{"simple host:port", "backend:8080", "simple"},
		{"IP:port", "192.168.1.1:8080", "simple"},
		{"multi-line", "upstream: a\nfallback: b", "yaml"},
		{"YAML key-value", "upstream: backend", "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectFormat(tt.input)
			if result != tt.expected {
				t.Errorf("detectFormat(%q) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsHostPort(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"backend:8080", true},
		{"192.168.1.1:80", true},
		{"localhost:443", true},
		{"upstream: backend", false},
		{"backend", false},
		{"http://backend:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isHostPort(tt.input)
			if result != tt.expected {
				t.Errorf("isHostPort(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestWeightedUpstream_GetEffectiveWeight(t *testing.T) {
	tests := []struct {
		name     string
		weight   int
		expected int
	}{
		{"zero weight", 0, 1},
		{"negative weight", -5, 1},
		{"positive weight", 50, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := WeightedUpstream{Weight: tt.weight}
			if got := u.GetEffectiveWeight(); got != tt.expected {
				t.Errorf("GetEffectiveWeight() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRouteConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		enabled  *bool
		expected bool
	}{
		{"nil (default true)", nil, true},
		{"explicitly true", boolPtr(true), true},
		{"explicitly false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &RouteConfig{Enabled: tt.enabled}
			if got := c.IsEnabled(); got != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRouteConfig_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		ttl       time.Duration
		updatedAt time.Time
		expected  bool
	}{
		{"zero TTL", 0, time.Now(), false},
		{"zero UpdatedAt", time.Hour, time.Time{}, false},
		{"not expired", time.Hour, time.Now(), false},
		{"expired", time.Millisecond, time.Now().Add(-time.Second), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &RouteConfig{TTL: tt.ttl, UpdatedAt: tt.updatedAt}
			if got := c.IsExpired(); got != tt.expected {
				t.Errorf("IsExpired() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRouteConfig_Clone(t *testing.T) {
	original := &RouteConfig{
		Version:      1,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Upstream:     "backend:8080",
		Fallback:     "random",
		Algorithm:    "round_robin",
		AlgorithmKey: "X-Tenant",
		TTL:          time.Hour,
		Description:  "test config",
		Enabled:      boolPtr(true),
		Rules: []Rule{
			{
				Match:     map[string]string{"header": "value"},
				Upstreams: []WeightedUpstream{{Address: "a:8080", Weight: 10}},
				Priority:  5,
			},
		},
		Metadata: map[string]string{"key": "value"},
	}

	clone := original.Clone()

	// Verify clone is not nil
	if clone == nil {
		t.Fatal("Clone returned nil")
	}

	// Verify basic fields
	if clone.Version != original.Version {
		t.Errorf("Version mismatch: got %d, want %d", clone.Version, original.Version)
	}
	if clone.Upstream != original.Upstream {
		t.Errorf("Upstream mismatch: got %s, want %s", clone.Upstream, original.Upstream)
	}

	// Verify deep copy of slices
	clone.Rules[0].Priority = 100
	if original.Rules[0].Priority == 100 {
		t.Error("Clone should not affect original rules")
	}

	// Verify deep copy of maps
	clone.Metadata["new_key"] = "new_value"
	if _, ok := original.Metadata["new_key"]; ok {
		t.Error("Clone should not affect original metadata")
	}
}

func TestRouteConfig_Clone_Nil(t *testing.T) {
	var c *RouteConfig
	clone := c.Clone()
	if clone != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestRouteConfig_WithVersion(t *testing.T) {
	original := &RouteConfig{
		Version:  1,
		Upstream: "backend:8080",
	}

	newVersion := original.WithVersion(2)

	// Original should not be modified
	if original.Version != 1 {
		t.Error("Original version should not be modified")
	}

	// New version should be set
	if newVersion.Version != 2 {
		t.Errorf("New version should be 2, got %d", newVersion.Version)
	}

	// UpdatedAt should be set
	if newVersion.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

func TestNewRouteConfig(t *testing.T) {
	config := NewRouteConfig()

	if config.Version != 1 {
		t.Errorf("Expected version 1, got %d", config.Version)
	}

	if config.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	if config.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

func boolPtr(b bool) *bool {
	return &b
}
