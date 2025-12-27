// Package matcher provides rule matching logic for dynamic routing.
package matcher

import (
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	lru "github.com/hashicorp/golang-lru"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

const (
	defaultRegexCacheSize    = 1024
	defaultSelectorCacheSize = 1024
)

// RuleMatcher matches routing rules against HTTP requests.
type RuleMatcher struct {
	// regexCache caches compiled regular expressions to avoid repeated compilation.
	regexCache *lru.Cache // map[string]*regexp.Regexp

	// selectorCache caches UpstreamSelectors per algorithm+key combination.
	selectorCache *lru.Cache // map[string]*UpstreamSelector
}

// NewRuleMatcher creates a new RuleMatcher.
func NewRuleMatcher() *RuleMatcher {
	return NewRuleMatcherWithCacheSizes(defaultRegexCacheSize, defaultSelectorCacheSize)
}

// NewRuleMatcherWithCacheSizes creates a new RuleMatcher with bounded caches.
// If a size is <= 0, a default size is used.
func NewRuleMatcherWithCacheSizes(regexCacheSize, selectorCacheSize int) *RuleMatcher {
	if regexCacheSize <= 0 {
		regexCacheSize = defaultRegexCacheSize
	}
	if selectorCacheSize <= 0 {
		selectorCacheSize = defaultSelectorCacheSize
	}

	regexCache, _ := lru.New(regexCacheSize)
	selectorCache, _ := lru.New(selectorCacheSize)
	return &RuleMatcher{
		regexCache:    regexCache,
		selectorCache: selectorCache,
	}
}

// getRegex retrieves a compiled regex from cache or compiles and caches it.
func (m *RuleMatcher) getRegex(pattern string) (*regexp.Regexp, error) {
	if m.regexCache != nil {
		if cached, ok := m.regexCache.Get(pattern); ok {
			return cached.(*regexp.Regexp), nil
		}
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	// Store in cache (may race with another goroutine, but that's ok)
	if m.regexCache != nil {
		m.regexCache.Add(pattern, re)
	}
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

	if m.selectorCache != nil {
		if cached, ok := m.selectorCache.Get(cacheKey); ok {
			return cached.(*UpstreamSelector)
		}
	}

	selector := NewUpstreamSelector(SelectionAlgorithm(algorithm), algorithmKey)
	if m.selectorCache != nil {
		m.selectorCache.Add(cacheKey, selector)
	}
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
// It primarily uses Caddy's replacer, but can fall back to the HTTP request
// for a small set of common placeholders to preserve flexibility.
func (m *RuleMatcher) resolvePlaceholder(r *http.Request, repl *caddy.Replacer, placeholder string) string {
	// Handle both with and without curly braces
	key := strings.Trim(placeholder, "{}")

	// Try to get from replacer
	if repl != nil {
		if val, ok := repl.GetString(key); ok {
			return val
		}
	}

	if r == nil {
		return ""
	}

	// Minimal request fallback for common placeholders.
	switch key {
	case "http.request.method":
		return r.Method
	case "http.request.host":
		return r.Host
	case "http.request.uri.path":
		if r.URL != nil {
			return r.URL.Path
		}
		return ""
	}
	if strings.HasPrefix(key, "http.request.header.") {
		h := strings.TrimPrefix(key, "http.request.header.")
		if h == "" {
			return ""
		}
		return r.Header.Get(h)
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
