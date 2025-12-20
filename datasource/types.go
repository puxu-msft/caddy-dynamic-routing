package datasource

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RouteConfig represents a routing configuration for a key.
type RouteConfig struct {
	// Version is the configuration version number.
	// Used for tracking changes and potential rollback.
	Version int64 `json:"version,omitempty" yaml:"version,omitempty"`

	// CreatedAt is when this configuration was first created.
	CreatedAt time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`

	// UpdatedAt is when this configuration was last updated.
	UpdatedAt time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`

	// Upstream is the direct upstream address for simple routing.
	// Used when no rules are defined.
	Upstream string `json:"upstream,omitempty" yaml:"upstream,omitempty"`

	// Rules is a list of conditional routing rules.
	// Rules are evaluated in priority order (highest first).
	Rules []Rule `json:"rules,omitempty" yaml:"rules,omitempty"`

	// Fallback is the fallback policy name when no rules match.
	// e.g., "random", "round_robin", "least_conn"
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"`

	// Algorithm is the load balancing algorithm to use when selecting from upstreams.
	// Supported values: weighted_random, round_robin, weighted_round_robin,
	// ip_hash, uri_hash, header_hash, least_conn, first, consistent_hash
	// Default is weighted_random.
	Algorithm string `json:"algorithm,omitempty" yaml:"algorithm,omitempty"`

	// AlgorithmKey is used with header_hash and consistent_hash algorithms.
	// For header_hash, this is the header name to hash.
	// For consistent_hash, this can be a header name to use as the hash key.
	AlgorithmKey string `json:"algorithm_key,omitempty" yaml:"algorithm_key,omitempty"`

	// Metadata holds additional metadata for logging/tracing.
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	// TTL is the time-to-live for this configuration.
	// After TTL expires, the configuration should be re-fetched.
	// If zero, no TTL is applied.
	TTL time.Duration `json:"ttl,omitempty" yaml:"ttl,omitempty"`

	// Enabled indicates whether this configuration is active.
	// Disabled configurations are ignored during routing.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// Description is a human-readable description of this configuration.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Rule represents a conditional routing rule.
type Rule struct {
	// Match is a map of conditions to match.
	// Key is a placeholder path (e.g., "http.request.header.X-Version"),
	// Value is the expected value or regex pattern.
	Match map[string]string `json:"match,omitempty" yaml:"match,omitempty"`

	// Upstreams is the list of weighted upstreams to use when the rule matches.
	Upstreams []WeightedUpstream `json:"upstreams" yaml:"upstreams"`

	// Priority determines the order of rule evaluation.
	// Higher priority rules are evaluated first.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`

	// Algorithm overrides the RouteConfig algorithm for this specific rule.
	// If empty, uses the RouteConfig.Algorithm.
	Algorithm string `json:"algorithm,omitempty" yaml:"algorithm,omitempty"`

	// AlgorithmKey overrides the RouteConfig algorithm key for this specific rule.
	AlgorithmKey string `json:"algorithm_key,omitempty" yaml:"algorithm_key,omitempty"`
}

// WeightedUpstream represents an upstream server with a weight.
type WeightedUpstream struct {
	// Address is the upstream address in the format "host:port".
	Address string `json:"address" yaml:"address"`

	// Weight is the relative weight for load balancing.
	// Default is 1 if not specified.
	Weight int `json:"weight,omitempty" yaml:"weight,omitempty"`
}

// ParseRouteConfig parses route configuration from raw bytes.
// It supports simple format (plain upstream address), JSON format, and YAML format.
func ParseRouteConfig(data []byte) (*RouteConfig, error) {
	return ParseRouteConfigWithFormat(data, "auto")
}

// ParseRouteConfigWithFormat parses route configuration with explicit format.
// Supported formats: "auto", "json", "yaml".
func ParseRouteConfigWithFormat(data []byte, format string) (*RouteConfig, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// Trim whitespace
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}

	// Determine format if auto
	if format == "auto" || format == "" {
		format = detectFormat(trimmed)
	}

	var config RouteConfig
	var err error

	switch format {
	case "json":
		// Check for JSON string first
		if trimmed[0] == '"' {
			var simpleUpstream string
			if err := json.Unmarshal(data, &simpleUpstream); err == nil {
				return &RouteConfig{Upstream: simpleUpstream}, nil
			}
		}
		err = json.Unmarshal(data, &config)
	case "yaml":
		err = yaml.Unmarshal(data, &config)
	default:
		// Treat as simple upstream address
		return &RouteConfig{Upstream: trimmed}, nil
	}

	if err != nil {
		// If parsing fails, treat as simple upstream
		return &RouteConfig{Upstream: trimmed}, nil
	}

	// Check if it's a valid config (has upstream or rules)
	if config.Upstream == "" && len(config.Rules) == 0 {
		// Empty config, treat as simple upstream
		return &RouteConfig{Upstream: trimmed}, nil
	}

	// Post-process the config
	normalizeConfig(&config)

	return &config, nil
}

// detectFormat detects the format of the configuration data.
func detectFormat(data string) string {
	if len(data) == 0 {
		return "simple"
	}

	// JSON starts with { or "
	if data[0] == '{' || data[0] == '"' || data[0] == '[' {
		return "json"
	}

	// YAML indicators: lines starting with key:, - (list item), or multiple lines
	if strings.Contains(data, "\n") {
		// Multi-line, likely YAML
		return "yaml"
	}

	// Check for YAML key: value pattern
	if strings.Contains(data, ":") && !strings.Contains(data, "://") {
		// Has colon but not a URL, could be YAML
		parts := strings.SplitN(data, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			// If the first part looks like a key (no spaces at start), it's likely YAML
			if !strings.HasPrefix(parts[0], " ") && !strings.HasPrefix(parts[0], "\t") {
				// But if it looks like host:port, treat as simple
				if isHostPort(data) {
					return "simple"
				}
				return "yaml"
			}
		}
	}

	return "simple"
}

// isHostPort checks if the string looks like a host:port address.
func isHostPort(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return false
	}
	// Check if second part is a number
	port := strings.TrimSpace(parts[1])
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(port) > 0
}

// normalizeConfig normalizes a parsed config (sorts rules, sets default weights).
func normalizeConfig(config *RouteConfig) {
	// Sort rules by priority (highest first)
	if len(config.Rules) > 0 {
		sort.Slice(config.Rules, func(i, j int) bool {
			return config.Rules[i].Priority > config.Rules[j].Priority
		})
	}
	// Set default weight for upstreams
	for i := range config.Rules {
		for j := range config.Rules[i].Upstreams {
			if config.Rules[i].Upstreams[j].Weight <= 0 {
				config.Rules[i].Upstreams[j].Weight = 1
			}
		}
	}
}

// GetEffectiveWeight returns the weight, defaulting to 1 if not set.
func (u WeightedUpstream) GetEffectiveWeight() int {
	if u.Weight <= 0 {
		return 1
	}
	return u.Weight
}

// IsEnabled returns whether the configuration is enabled.
// Defaults to true if Enabled is nil.
func (c *RouteConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// IsExpired returns whether the configuration has expired based on TTL.
// Returns false if TTL is zero or UpdatedAt is zero.
func (c *RouteConfig) IsExpired() bool {
	if c.TTL == 0 || c.UpdatedAt.IsZero() {
		return false
	}
	return time.Since(c.UpdatedAt) > c.TTL
}

// Clone creates a deep copy of the RouteConfig.
func (c *RouteConfig) Clone() *RouteConfig {
	if c == nil {
		return nil
	}

	clone := &RouteConfig{
		Version:      c.Version,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
		Upstream:     c.Upstream,
		Fallback:     c.Fallback,
		Algorithm:    c.Algorithm,
		AlgorithmKey: c.AlgorithmKey,
		TTL:          c.TTL,
		Description:  c.Description,
	}

	if c.Enabled != nil {
		enabled := *c.Enabled
		clone.Enabled = &enabled
	}

	if c.Rules != nil {
		clone.Rules = make([]Rule, len(c.Rules))
		for i, rule := range c.Rules {
			clone.Rules[i] = Rule{
				Priority:     rule.Priority,
				Algorithm:    rule.Algorithm,
				AlgorithmKey: rule.AlgorithmKey,
			}
			if rule.Match != nil {
				clone.Rules[i].Match = make(map[string]string, len(rule.Match))
				for k, v := range rule.Match {
					clone.Rules[i].Match[k] = v
				}
			}
			if rule.Upstreams != nil {
				clone.Rules[i].Upstreams = make([]WeightedUpstream, len(rule.Upstreams))
				copy(clone.Rules[i].Upstreams, rule.Upstreams)
			}
		}
	}

	if c.Metadata != nil {
		clone.Metadata = make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			clone.Metadata[k] = v
		}
	}

	return clone
}

// WithVersion returns a copy of the config with the specified version.
func (c *RouteConfig) WithVersion(version int64) *RouteConfig {
	clone := c.Clone()
	clone.Version = version
	clone.UpdatedAt = time.Now()
	return clone
}

// NewRouteConfig creates a new RouteConfig with the current timestamp.
func NewRouteConfig() *RouteConfig {
	now := time.Now()
	return &RouteConfig{
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
