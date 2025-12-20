// Package matcher provides rule matching logic for dynamic routing.
package matcher

import (
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

// RuleMatcher matches routing rules against HTTP requests.
type RuleMatcher struct {
	// regexCache caches compiled regular expressions to avoid repeated compilation.
	regexCache sync.Map // map[string]*regexp.Regexp

	// selectorCache caches UpstreamSelectors per algorithm+key combination.
	selectorCache sync.Map // map[string]*UpstreamSelector
}

// NewRuleMatcher creates a new RuleMatcher.
func NewRuleMatcher() *RuleMatcher {
	return &RuleMatcher{}
}

// getRegex retrieves a compiled regex from cache or compiles and caches it.
func (m *RuleMatcher) getRegex(pattern string) (*regexp.Regexp, error) {
	if cached, ok := m.regexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	// Store in cache (may race with another goroutine, but that's ok)
	m.regexCache.Store(pattern, re)
	return re, nil
}

// selectorCacheKeyBuilder is a pool for building selector cache keys.
var selectorCacheKeyBuilder = sync.Pool{
	New: func() interface{} {
		return new(strings.Builder)
	},
}

// getSelector retrieves or creates an UpstreamSelector for the given algorithm and key.
// Uses pooled string builder to reduce allocations.
func (m *RuleMatcher) getSelector(algorithm, algorithmKey string) *UpstreamSelector {
	// Build cache key using pooled builder to reduce allocations
	sb := selectorCacheKeyBuilder.Get().(*strings.Builder)
	sb.Reset()
	sb.WriteString(algorithm)
	sb.WriteByte(':')
	sb.WriteString(algorithmKey)
	cacheKey := sb.String()
	selectorCacheKeyBuilder.Put(sb)

	if cached, ok := m.selectorCache.Load(cacheKey); ok {
		return cached.(*UpstreamSelector)
	}

	selector := NewUpstreamSelector(SelectionAlgorithm(algorithm), algorithmKey)
	m.selectorCache.Store(cacheKey, selector)
	return selector
}

// Match finds the best matching upstream for the request based on the route config.
// Returns nil if no match is found.
func (m *RuleMatcher) Match(r *http.Request, repl *caddy.Replacer, config *datasource.RouteConfig) *datasource.WeightedUpstream {
	if config == nil {
		return nil
	}

	// Simple mode: direct upstream
	if config.Upstream != "" && len(config.Rules) == 0 {
		return &datasource.WeightedUpstream{
			Address: config.Upstream,
			Weight:  1,
		}
	}

	// Default algorithm
	defaultAlgorithm := config.Algorithm
	if defaultAlgorithm == "" {
		defaultAlgorithm = string(AlgorithmWeightedRandom)
	}
	defaultAlgorithmKey := config.AlgorithmKey

	// Advanced mode: evaluate rules (already sorted by priority)
	for _, rule := range config.Rules {
		if m.matchConditions(r, repl, rule.Match) {
			// Determine algorithm for this rule
			algorithm := rule.Algorithm
			if algorithm == "" {
				algorithm = defaultAlgorithm
			}
			algorithmKey := rule.AlgorithmKey
			if algorithmKey == "" {
				algorithmKey = defaultAlgorithmKey
			}

			return m.selectUpstream(r, rule.Upstreams, algorithm, algorithmKey)
		}
	}

	return nil
}

// selectUpstream selects an upstream using the specified algorithm.
func (m *RuleMatcher) selectUpstream(r *http.Request, upstreams []datasource.WeightedUpstream, algorithm, algorithmKey string) *datasource.WeightedUpstream {
	if len(upstreams) == 0 {
		return nil
	}

	if len(upstreams) == 1 {
		return &upstreams[0]
	}

	selector := m.getSelector(algorithm, algorithmKey)
	return selector.Select(r, upstreams)
}

// matchConditions checks if all conditions in the match map are satisfied.
// Optimized to check exact matches first (faster) before regex patterns.
func (m *RuleMatcher) matchConditions(r *http.Request, repl *caddy.Replacer, conditions map[string]string) bool {
	if len(conditions) == 0 {
		// No conditions means always match
		return true
	}

	// Optimization: Process exact matches first (fast), then regex patterns (slow).
	// This enables fast-fail for most common cases.
	var regexConditions [][2]string // [placeholder, expected]

	for placeholder, expected := range conditions {
		// Defer regex patterns to the second pass
		if strings.HasPrefix(expected, "~") {
			regexConditions = append(regexConditions, [2]string{placeholder, expected})
			continue
		}

		actual := m.resolvePlaceholder(r, repl, placeholder)
		if actual != expected {
			return false
		}
	}

	// Second pass: regex patterns (only if all exact matches passed)
	for _, cond := range regexConditions {
		actual := m.resolvePlaceholder(r, repl, cond[0])
		if !m.matchRegex(actual, cond[1]) {
			return false
		}
	}

	return true
}

// matchRegex checks if the actual value matches the regex pattern.
// The expected string should start with "~".
func (m *RuleMatcher) matchRegex(actual, expected string) bool {
	pattern := strings.TrimPrefix(expected, "~")
	re, err := m.getRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(actual)
}

// resolvePlaceholder resolves a placeholder path to its value.
func (m *RuleMatcher) resolvePlaceholder(r *http.Request, repl *caddy.Replacer, placeholder string) string {
	if repl == nil {
		return ""
	}

	// Handle both with and without curly braces
	key := strings.Trim(placeholder, "{}")

	// Try to get from replacer
	if val, ok := repl.GetString(key); ok {
		return val
	}

	return ""
}

// matchValue checks if the actual value matches the expected pattern.
// Supports exact match and regex patterns (prefixed with "~").
func (m *RuleMatcher) matchValue(actual, expected string) bool {
	if expected == "" {
		return actual == ""
	}

	// Check for regex pattern
	if strings.HasPrefix(expected, "~") {
		pattern := strings.TrimPrefix(expected, "~")
		re, err := m.getRegex(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	}

	// Exact match
	return actual == expected
}
