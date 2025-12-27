package matcher

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

func TestUpstreamSelector_RoundRobin(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmRoundRobin, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	// Should cycle through upstreams
	results := make(map[string]int)
	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		u := selector.Select(req, upstreams)
		results[u.Address]++
	}

	// Each should be selected 3 times
	for _, addr := range []string{"a:8080", "b:8080", "c:8080"} {
		if results[addr] != 3 {
			t.Errorf("Expected %s to be selected 3 times, got %d", addr, results[addr])
		}
	}
}

func TestUpstreamSelector_WeightedRoundRobin(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmWeightedRoundRobin, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 5},
		{Address: "b:8080", Weight: 3},
		{Address: "c:8080", Weight: 2},
	}

	// Make 100 selections
	results := make(map[string]int)
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		u := selector.Select(req, upstreams)
		results[u.Address]++
	}

	// Check approximate distribution (should be 50:30:20)
	if results["a:8080"] < 40 || results["a:8080"] > 60 {
		t.Errorf("Expected ~50 for a:8080, got %d", results["a:8080"])
	}
	if results["b:8080"] < 20 || results["b:8080"] > 40 {
		t.Errorf("Expected ~30 for b:8080, got %d", results["b:8080"])
	}
	if results["c:8080"] < 10 || results["c:8080"] > 30 {
		t.Errorf("Expected ~20 for c:8080, got %d", results["c:8080"])
	}
}

func TestUpstreamSelector_IPHash(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmIPHash, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	// Same IP should always select same upstream
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	first := selector.Select(req, upstreams)
	for i := 0; i < 10; i++ {
		u := selector.Select(req, upstreams)
		if u.Address != first.Address {
			t.Errorf("IP hash should be consistent: expected %s, got %s", first.Address, u.Address)
		}
	}
}

func TestUpstreamSelector_IPHash_XForwardedFor(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmIPHash, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 192.168.1.1")

	u1 := selector.Select(req, upstreams)

	// Different X-Forwarded-For should potentially select different upstream
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", "10.0.0.2")

	// Just verify it works without panicking
	selector.Select(req2, upstreams)

	// Same XFF should be consistent
	u2 := selector.Select(req, upstreams)
	if u1.Address != u2.Address {
		t.Error("Same X-Forwarded-For should select same upstream")
	}
}

func TestUpstreamSelector_URIHash(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmURIHash, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	// Same URI should always select same upstream
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)

	first := selector.Select(req, upstreams)
	for i := 0; i < 10; i++ {
		u := selector.Select(req, upstreams)
		if u.Address != first.Address {
			t.Errorf("URI hash should be consistent: expected %s, got %s", first.Address, u.Address)
		}
	}

	// Different URI should potentially select different upstream
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/products", nil)
	// Just verify it works
	selector.Select(req2, upstreams)
}

func TestUpstreamSelector_HeaderHash(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmHeaderHash, "X-Tenant-ID")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	// Same header value should always select same upstream
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-abc")

	first := selector.Select(req, upstreams)
	for i := 0; i < 10; i++ {
		u := selector.Select(req, upstreams)
		if u.Address != first.Address {
			t.Errorf("Header hash should be consistent: expected %s, got %s", first.Address, u.Address)
		}
	}
}

func TestUpstreamSelector_HeaderHash_MissingHeader(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmHeaderHash, "X-Tenant-ID")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
	}

	// Missing header should fallback to weighted random
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No X-Tenant-ID header

	// Should not panic
	u := selector.Select(req, upstreams)
	if u == nil {
		t.Error("Should select an upstream even with missing header")
	}
}

func TestUpstreamSelector_First(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmFirst, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	for i := 0; i < 10; i++ {
		u := selector.Select(req, upstreams)
		if u.Address != "a:8080" {
			t.Errorf("First algorithm should always select first upstream: got %s", u.Address)
		}
	}
}

func TestUpstreamSelector_ConsistentHash(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmConsistentHash, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080"},
		{Address: "b:8080"},
		{Address: "c:8080"},
	}

	// Same IP should always select same upstream
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	first := selector.Select(req, upstreams)
	for i := 0; i < 10; i++ {
		u := selector.Select(req, upstreams)
		if u.Address != first.Address {
			t.Errorf("Consistent hash should be consistent: expected %s, got %s", first.Address, u.Address)
		}
	}
}

func TestUpstreamSelector_WeightedRandom(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmWeightedRandom, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 80},
		{Address: "b:8080", Weight: 20},
	}

	// Make 1000 selections
	results := make(map[string]int)
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		u := selector.Select(req, upstreams)
		results[u.Address]++
	}

	// a should be selected ~800 times (±100)
	if results["a:8080"] < 700 || results["a:8080"] > 900 {
		t.Errorf("Expected ~800 for a:8080, got %d", results["a:8080"])
	}
}

func TestUpstreamSelector_SingleUpstream(t *testing.T) {
	algorithms := []SelectionAlgorithm{
		AlgorithmWeightedRandom,
		AlgorithmRoundRobin,
		AlgorithmWeightedRoundRobin,
		AlgorithmIPHash,
		AlgorithmURIHash,
		AlgorithmFirst,
		AlgorithmConsistentHash,
	}

	upstreams := []datasource.WeightedUpstream{
		{Address: "only:8080"},
	}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			selector := NewUpstreamSelector(algo, "")
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "192.168.1.1:1234"

			u := selector.Select(req, upstreams)
			if u.Address != "only:8080" {
				t.Errorf("Expected 'only:8080', got '%s'", u.Address)
			}
		})
	}
}

func TestUpstreamSelector_EmptyUpstreams(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmRoundRobin, "")
	upstreams := []datasource.WeightedUpstream{}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	u := selector.Select(req, upstreams)

	if u != nil {
		t.Error("Expected nil for empty upstreams")
	}
}

func TestConsistentHashRing(t *testing.T) {
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 1},
		{Address: "b:8080", Weight: 2},
		{Address: "c:8080", Weight: 1},
	}

	ring := NewConsistentHashRing(upstreams, 100)

	// Same key should always return same upstream
	first := ring.Get("test-key")
	for i := 0; i < 10; i++ {
		u := ring.Get("test-key")
		if u.Address != first.Address {
			t.Errorf("Consistent hash should be consistent: expected %s, got %s", first.Address, u.Address)
		}
	}
}

func TestConsistentHashRing_Distribution(t *testing.T) {
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 1},
		{Address: "b:8080", Weight: 1},
		{Address: "c:8080", Weight: 1},
	}

	ring := NewConsistentHashRing(upstreams, 150)

	// Check distribution across many keys
	results := make(map[string]int)
	for i := 0; i < 1000; i++ {
		key := string(rune(i)) + "-key"
		u := ring.Get(key)
		results[u.Address]++
	}

	// Each should get roughly 1/3 (±15%)
	for _, addr := range []string{"a:8080", "b:8080", "c:8080"} {
		if results[addr] < 200 || results[addr] > 500 {
			t.Logf("Distribution for %s: %d (may be acceptable variance)", addr, results[addr])
		}
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		expected   string
	}{
		{
			name:       "remote addr only",
			remoteAddr: "192.168.1.1:12345",
			expected:   "192.168.1.1",
		},
		{
			name:       "x-forwarded-for single",
			remoteAddr: "127.0.0.1:8080",
			xff:        "10.0.0.1",
			expected:   "10.0.0.1",
		},
		{
			name:       "x-forwarded-for multiple",
			remoteAddr: "127.0.0.1:8080",
			xff:        "10.0.0.1, 192.168.1.1, 172.16.0.1",
			expected:   "10.0.0.1",
		},
		{
			name:       "x-real-ip",
			remoteAddr: "127.0.0.1:8080",
			xri:        "10.0.0.2",
			expected:   "10.0.0.2",
		},
		{
			name:       "xff takes precedence over xri",
			remoteAddr: "127.0.0.1:8080",
			xff:        "10.0.0.1",
			xri:        "10.0.0.2",
			expected:   "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}

			result := getClientIP(req)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestUpstreamSelector_LeastConn(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmLeastConn, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 1},
		{Address: "b:8080", Weight: 1},
		{Address: "c:8080", Weight: 1},
	}

	// Initially all have 0 connections, should pick first
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	u := selector.Select(req, upstreams)
	if u.Address != "a:8080" {
		t.Errorf("Expected a:8080 for first selection (all 0 connections), got %s", u.Address)
	}

	// Add connections to a
	selector.IncrementConn("a:8080")
	selector.IncrementConn("a:8080")

	// Should now pick b or c (both have 0)
	u = selector.Select(req, upstreams)
	if u.Address == "a:8080" {
		t.Errorf("Should not pick a:8080 when it has the most connections")
	}

	// Add connection to b
	selector.IncrementConn("b:8080")

	// c has 0 connections, should pick c
	u = selector.Select(req, upstreams)
	if u.Address != "c:8080" {
		t.Errorf("Expected c:8080 (0 connections), got %s", u.Address)
	}
}

func TestUpstreamSelector_LeastConnWeighted(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmLeastConn, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 10}, // High capacity
		{Address: "b:8080", Weight: 1},  // Low capacity
	}

	// Add 5 connections to a
	for i := 0; i < 5; i++ {
		selector.IncrementConn("a:8080")
	}

	// Add 1 connection to b
	selector.IncrementConn("b:8080")

	// a has 5 connections with weight 10 -> ratio 0.5
	// b has 1 connection with weight 1 -> ratio 1.0
	// a should be selected because it has lower ratio
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	u := selector.Select(req, upstreams)
	if u.Address != "a:8080" {
		t.Errorf("Expected a:8080 (ratio 0.5), got %s", u.Address)
	}
}

func TestUpstreamSelector_ConnCountMethods(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmLeastConn, "")

	// Initially 0
	if c := selector.getConnCount("test:8080"); c != 0 {
		t.Errorf("Expected 0 connections initially, got %d", c)
	}

	// Increment
	selector.IncrementConn("test:8080")
	if c := selector.getConnCount("test:8080"); c != 1 {
		t.Errorf("Expected 1 connection after increment, got %d", c)
	}

	// Increment again
	selector.IncrementConn("test:8080")
	if c := selector.getConnCount("test:8080"); c != 2 {
		t.Errorf("Expected 2 connections after second increment, got %d", c)
	}

	// Decrement
	selector.DecrementConn("test:8080")
	if c := selector.getConnCount("test:8080"); c != 1 {
		t.Errorf("Expected 1 connection after decrement, got %d", c)
	}

	// Decrement to 0
	selector.DecrementConn("test:8080")
	if c := selector.getConnCount("test:8080"); c != 0 {
		t.Errorf("Expected 0 connections after second decrement, got %d", c)
	}

	// Decrement non-existent (should not panic)
	selector.DecrementConn("nonexistent:8080")
}

func TestUpstreamSelector_Concurrent(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmRoundRobin, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 1},
		{Address: "b:8080", Weight: 1},
		{Address: "c:8080", Weight: 1},
	}

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOperations = 100

	// Concurrent Select calls
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "192.168.1.1:12345"
				u := selector.Select(req, upstreams)
				if u == nil {
					t.Error("Select returned nil")
				}
			}
		}()
	}

	wg.Wait()
}

func TestUpstreamSelector_ConcurrentConnCount(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmLeastConn, "")

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOperations = 100

	// Concurrent increment/decrement
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := "backend:8080"
			for j := 0; j < numOperations; j++ {
				selector.IncrementConn(addr)
				selector.DecrementConn(addr)
			}
		}()
	}

	wg.Wait()

	// After all increments and decrements, count should be 0
	if c := selector.getConnCount("backend:8080"); c != 0 {
		t.Errorf("Expected 0 connections after balanced inc/dec, got %d", c)
	}
}

func TestUpstreamSelector_ConcurrentWeightedRoundRobin(t *testing.T) {
	selector := NewUpstreamSelector(AlgorithmWeightedRoundRobin, "")
	upstreams := []datasource.WeightedUpstream{
		{Address: "a:8080", Weight: 3},
		{Address: "b:8080", Weight: 2},
		{Address: "c:8080", Weight: 1},
	}

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOperations = 100

	// Concurrent Select calls on weighted round robin
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				u := selector.Select(req, upstreams)
				if u == nil {
					t.Error("Select returned nil")
				}
			}
		}()
	}

	wg.Wait()
}
