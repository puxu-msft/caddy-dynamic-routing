// Package cache provides a generic LRU cache wrapper for route configurations.
package cache

import (
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

// DefaultMaxSize is the default maximum number of entries in the cache.
const DefaultMaxSize = 10000

// DefaultNegativeTTL is the default TTL for negative cache entries.
const DefaultNegativeTTL = 30 * time.Second

// RouteCache is a thread-safe LRU cache for route configurations.
// Note: hashicorp/golang-lru.Cache is already thread-safe, so no external locking is needed.
type RouteCache struct {
	cache       *lru.Cache
	maxSize     int
	negativeTTL time.Duration

	// Stats
	hits         atomic.Uint64
	misses       atomic.Uint64
	negativeHits atomic.Uint64

	// Negative cache for missing keys (separate from main cache to avoid type issues)
	negativeCache sync.Map // map[string]time.Time (expiration time)
}

// NewRouteCache creates a new RouteCache with the specified maximum size.
// If maxSize <= 0, DefaultMaxSize is used.
func NewRouteCache(maxSize int) *RouteCache {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}

	cache, _ := lru.New(maxSize)
	return &RouteCache{
		cache:       cache,
		maxSize:     maxSize,
		negativeTTL: DefaultNegativeTTL,
	}
}

// Get retrieves a route configuration from the cache.
// Returns nil if the key is not found or is a negative cache entry.
func (c *RouteCache) Get(key string) *datasource.RouteConfig {
	// Check negative cache first
	if exp, ok := c.negativeCache.Load(key); ok {
		if time.Now().Before(exp.(time.Time)) {
			c.negativeHits.Add(1)
			c.hits.Add(1)
			return nil // Still valid negative entry
		}
		// Expired, remove it
		c.negativeCache.Delete(key)
	}

	if val, ok := c.cache.Get(key); ok {
		c.hits.Add(1)
		return val.(*datasource.RouteConfig)
	}
	c.misses.Add(1)
	return nil
}

// IsNegativeCached returns true if the key is in the negative cache.
func (c *RouteCache) IsNegativeCached(key string) bool {
	if exp, ok := c.negativeCache.Load(key); ok {
		if time.Now().Before(exp.(time.Time)) {
			return true
		}
		c.negativeCache.Delete(key)
	}
	return false
}

// SetNegative caches a negative (not found) result for the key.
// This prevents repeated lookups for non-existent keys.
func (c *RouteCache) SetNegative(key string) {
	c.negativeCache.Store(key, time.Now().Add(c.negativeTTL))
}

// SetNegativeTTL sets the TTL for negative cache entries.
func (c *RouteCache) SetNegativeTTL(ttl time.Duration) {
	c.negativeTTL = ttl
}

// Set stores a route configuration in the cache.
// Also clears any negative cache entry for the same key.
func (c *RouteCache) Set(key string, config *datasource.RouteConfig) {
	c.negativeCache.Delete(key) // Clear negative cache
	c.cache.Add(key, config)
}

// Delete removes a route configuration from the cache.
// Also clears any negative cache entry for the same key.
func (c *RouteCache) Delete(key string) {
	c.negativeCache.Delete(key) // Clear negative cache
	c.cache.Remove(key)
}

// Clear removes all entries from the cache.
func (c *RouteCache) Clear() {
	c.cache.Purge()
	// Clear negative cache
	c.negativeCache.Range(func(k, _ any) bool {
		c.negativeCache.Delete(k)
		return true
	})
	// Reset stats
	c.hits.Store(0)
	c.misses.Store(0)
	c.negativeHits.Store(0)
}

// Len returns the number of entries in the cache.
func (c *RouteCache) Len() int {
	return c.cache.Len()
}

// MaxSize returns the configured max size for the cache.
func (c *RouteCache) MaxSize() int {
	return c.maxSize
}

// Stats returns a snapshot of cache hit/miss counters.
func (c *RouteCache) Stats() (hits, misses, negativeHits uint64, hitRate float64) {
	h := c.hits.Load()
	m := c.misses.Load()
	nh := c.negativeHits.Load()
	total := h + m
	if total == 0 {
		return h, m, nh, 0
	}
	return h, m, nh, float64(h) / float64(total)
}

// Keys returns all keys in the cache.
func (c *RouteCache) Keys() []string {
	keys := c.cache.Keys()
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		if key, ok := k.(string); ok {
			result = append(result, key)
		}
	}
	return result
}

// Range iterates over all entries in the cache.
// If f returns false, iteration stops.
func (c *RouteCache) Range(f func(key string, config *datasource.RouteConfig) bool) {
	for _, key := range c.cache.Keys() {
		k, ok := key.(string)
		if !ok {
			continue
		}
		if val, ok := c.cache.Peek(k); ok {
			if config, ok := val.(*datasource.RouteConfig); ok {
				if !f(k, config) {
					break
				}
			}
		}
	}
}
