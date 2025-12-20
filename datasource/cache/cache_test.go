package cache

import (
	"sync"
	"testing"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

func TestNewRouteCache(t *testing.T) {
	tests := []struct {
		name     string
		maxSize  int
		wantSize int
	}{
		{"default size", 0, DefaultMaxSize},
		{"negative size", -1, DefaultMaxSize},
		{"custom size", 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewRouteCache(tt.maxSize)
			if cache == nil {
				t.Fatal("NewRouteCache returned nil")
			}
			if cache.maxSize != tt.wantSize {
				t.Errorf("maxSize = %d, want %d", cache.maxSize, tt.wantSize)
			}
		})
	}
}

func TestRouteCache_GetSet(t *testing.T) {
	cache := NewRouteCache(100)

	// Test Get on empty cache
	if got := cache.Get("nonexistent"); got != nil {
		t.Errorf("Get(nonexistent) = %v, want nil", got)
	}

	// Test Set and Get
	config := &datasource.RouteConfig{
		Upstream: "backend:8080",
		Version:  1,
	}
	cache.Set("key1", config)

	got := cache.Get("key1")
	if got == nil {
		t.Fatal("Get(key1) returned nil after Set")
	}
	if got.Upstream != config.Upstream {
		t.Errorf("Upstream = %s, want %s", got.Upstream, config.Upstream)
	}
	if got.Version != config.Version {
		t.Errorf("Version = %d, want %d", got.Version, config.Version)
	}
}

func TestRouteCache_Delete(t *testing.T) {
	cache := NewRouteCache(100)

	config := &datasource.RouteConfig{Upstream: "backend:8080"}
	cache.Set("key1", config)

	// Verify it exists
	if cache.Get("key1") == nil {
		t.Fatal("key1 should exist before delete")
	}

	// Delete
	cache.Delete("key1")

	// Verify it's gone
	if cache.Get("key1") != nil {
		t.Error("key1 should not exist after delete")
	}

	// Delete non-existent key should not panic
	cache.Delete("nonexistent")
}

func TestRouteCache_Clear(t *testing.T) {
	cache := NewRouteCache(100)

	// Add multiple entries
	for i := 0; i < 10; i++ {
		cache.Set(string(rune('a'+i)), &datasource.RouteConfig{Upstream: "backend"})
	}

	if cache.Len() != 10 {
		t.Errorf("Len() = %d, want 10", cache.Len())
	}

	// Clear
	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("Len() after Clear = %d, want 0", cache.Len())
	}
}

func TestRouteCache_Len(t *testing.T) {
	cache := NewRouteCache(100)

	if cache.Len() != 0 {
		t.Errorf("Len() on empty cache = %d, want 0", cache.Len())
	}

	cache.Set("key1", &datasource.RouteConfig{})
	if cache.Len() != 1 {
		t.Errorf("Len() = %d, want 1", cache.Len())
	}

	cache.Set("key2", &datasource.RouteConfig{})
	if cache.Len() != 2 {
		t.Errorf("Len() = %d, want 2", cache.Len())
	}

	cache.Delete("key1")
	if cache.Len() != 1 {
		t.Errorf("Len() after delete = %d, want 1", cache.Len())
	}
}

func TestRouteCache_Keys(t *testing.T) {
	cache := NewRouteCache(100)

	// Empty cache
	keys := cache.Keys()
	if len(keys) != 0 {
		t.Errorf("Keys() on empty cache = %v, want empty", keys)
	}

	// Add entries
	cache.Set("key1", &datasource.RouteConfig{})
	cache.Set("key2", &datasource.RouteConfig{})
	cache.Set("key3", &datasource.RouteConfig{})

	keys = cache.Keys()
	if len(keys) != 3 {
		t.Errorf("len(Keys()) = %d, want 3", len(keys))
	}

	// Check all keys present
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	for _, expected := range []string{"key1", "key2", "key3"} {
		if !keySet[expected] {
			t.Errorf("Key %s not found in Keys()", expected)
		}
	}
}

func TestRouteCache_Range(t *testing.T) {
	cache := NewRouteCache(100)

	// Add entries
	cache.Set("key1", &datasource.RouteConfig{Upstream: "backend1"})
	cache.Set("key2", &datasource.RouteConfig{Upstream: "backend2"})
	cache.Set("key3", &datasource.RouteConfig{Upstream: "backend3"})

	// Range all
	visited := make(map[string]string)
	cache.Range(func(key string, config *datasource.RouteConfig) bool {
		visited[key] = config.Upstream
		return true
	})

	if len(visited) != 3 {
		t.Errorf("Range visited %d entries, want 3", len(visited))
	}

	// Range with early stop
	count := 0
	cache.Range(func(key string, config *datasource.RouteConfig) bool {
		count++
		return count < 2 // Stop after 2
	})

	if count != 2 {
		t.Errorf("Range with early stop visited %d entries, want 2", count)
	}
}

func TestRouteCache_LRUEviction(t *testing.T) {
	cache := NewRouteCache(3) // Small cache

	// Fill cache
	cache.Set("key1", &datasource.RouteConfig{Upstream: "backend1"})
	cache.Set("key2", &datasource.RouteConfig{Upstream: "backend2"})
	cache.Set("key3", &datasource.RouteConfig{Upstream: "backend3"})

	// Access key1 to make it recently used
	cache.Get("key1")

	// Add new entry, should evict key2 (least recently used)
	cache.Set("key4", &datasource.RouteConfig{Upstream: "backend4"})

	if cache.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (max size)", cache.Len())
	}

	// key1 should still exist (was accessed)
	if cache.Get("key1") == nil {
		t.Error("key1 should still exist after eviction")
	}

	// key4 should exist (just added)
	if cache.Get("key4") == nil {
		t.Error("key4 should exist")
	}
}

func TestRouteCache_Concurrent(t *testing.T) {
	cache := NewRouteCache(1000)

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOperations = 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('A' + id))
				cache.Set(key, &datasource.RouteConfig{
					Upstream: "backend",
					Version:  int64(j),
				})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('A' + id))
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent deletes
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('A' + id))
				cache.Delete(key)
			}
		}(i)
	}

	wg.Wait()

	// Should not panic and cache should be consistent
	_ = cache.Len()
	_ = cache.Keys()
}
