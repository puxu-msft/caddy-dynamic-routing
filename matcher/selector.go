// Package matcher provides load balancing algorithms for upstream selection.
package matcher

import (
	"fmt"
	"hash"
	"hash/fnv"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

// fnvPool provides pooled FNV hashers to reduce allocations.
var fnvPool = sync.Pool{
	New: func() interface{} {
		return fnv.New32a()
	},
}

// SelectionAlgorithm represents a load balancing algorithm.
type SelectionAlgorithm string

const (
	// AlgorithmWeightedRandom selects upstreams based on their weights randomly.
	AlgorithmWeightedRandom SelectionAlgorithm = "weighted_random"

	// AlgorithmRoundRobin selects upstreams in a round-robin fashion.
	AlgorithmRoundRobin SelectionAlgorithm = "round_robin"

	// AlgorithmWeightedRoundRobin selects upstreams in weighted round-robin fashion.
	AlgorithmWeightedRoundRobin SelectionAlgorithm = "weighted_round_robin"

	// AlgorithmIPHash selects upstreams based on client IP hash.
	AlgorithmIPHash SelectionAlgorithm = "ip_hash"

	// AlgorithmURIHash selects upstreams based on request URI hash.
	AlgorithmURIHash SelectionAlgorithm = "uri_hash"

	// AlgorithmHeaderHash selects upstreams based on a specific header hash.
	AlgorithmHeaderHash SelectionAlgorithm = "header_hash"

	// AlgorithmLeastConn selects the upstream with least connections.
	// Note: This requires external connection tracking, simplified here.
	AlgorithmLeastConn SelectionAlgorithm = "least_conn"

	// AlgorithmFirst always selects the first upstream.
	AlgorithmFirst SelectionAlgorithm = "first"

	// AlgorithmConsistentHash uses consistent hashing for stable distribution.
	AlgorithmConsistentHash SelectionAlgorithm = "consistent_hash"
)

// UpstreamSelector provides algorithm-based upstream selection.
type UpstreamSelector struct {
	algorithm SelectionAlgorithm
	hashKey   string // For header_hash algorithm

	// Round-robin state
	rrIndex uint64

	// Weighted round-robin state
	wrrMu        sync.Mutex
	wrrWeights   []int
	wrrCurrent   []int
	wrrUpstreams []datasource.WeightedUpstream

	// Consistent hash ring
	hashRing *ConsistentHashRing

	// Least connections state
	connCounts sync.Map // map[string]*int64 - address to connection count
}

// NewUpstreamSelector creates a new UpstreamSelector with the given algorithm.
func NewUpstreamSelector(algorithm SelectionAlgorithm, hashKey string) *UpstreamSelector {
	return &UpstreamSelector{
		algorithm: algorithm,
		hashKey:   hashKey,
	}
}

// Select selects an upstream from the list using the configured algorithm.
func (s *UpstreamSelector) Select(r *http.Request, upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	if len(upstreams) == 0 {
		return nil
	}

	if len(upstreams) == 1 {
		return &upstreams[0]
	}

	switch s.algorithm {
	case AlgorithmRoundRobin:
		return s.selectRoundRobin(upstreams)
	case AlgorithmWeightedRoundRobin:
		return s.selectWeightedRoundRobin(upstreams)
	case AlgorithmIPHash:
		return s.selectIPHash(r, upstreams)
	case AlgorithmURIHash:
		return s.selectURIHash(r, upstreams)
	case AlgorithmHeaderHash:
		return s.selectHeaderHash(r, upstreams)
	case AlgorithmFirst:
		return s.selectFirst(upstreams)
	case AlgorithmConsistentHash:
		return s.selectConsistentHash(r, upstreams)
	case AlgorithmLeastConn:
		return s.selectLeastConn(upstreams)
	case AlgorithmWeightedRandom:
		fallthrough
	default:
		return s.selectWeightedRandom(upstreams)
	}
}

// selectRoundRobin selects upstreams in round-robin fashion.
func (s *UpstreamSelector) selectRoundRobin(upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	idx := atomic.AddUint64(&s.rrIndex, 1) - 1
	return &upstreams[idx%uint64(len(upstreams))]
}

// selectWeightedRoundRobin implements weighted round-robin selection.
// Uses the smooth weighted round-robin algorithm.
func (s *UpstreamSelector) selectWeightedRoundRobin(upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	s.wrrMu.Lock()
	defer s.wrrMu.Unlock()

	// Reset if upstreams changed
	if !s.wrrUpstreamsMatch(upstreams) {
		s.initWeightedRoundRobin(upstreams)
	}

	// Find the upstream with highest current weight
	totalWeight := 0
	maxIdx := 0
	maxWeight := s.wrrCurrent[0]

	for i := range upstreams {
		s.wrrCurrent[i] += s.wrrWeights[i]
		totalWeight += s.wrrWeights[i]

		if s.wrrCurrent[i] > maxWeight {
			maxWeight = s.wrrCurrent[i]
			maxIdx = i
		}
	}

	// Reduce the selected upstream's current weight
	s.wrrCurrent[maxIdx] -= totalWeight

	return &upstreams[maxIdx]
}

func (s *UpstreamSelector) wrrUpstreamsMatch(upstreams []datasource.WeightedUpstream) bool {
	if len(s.wrrUpstreams) != len(upstreams) {
		return false
	}
	for i, u := range upstreams {
		if s.wrrUpstreams[i].Address != u.Address || s.wrrUpstreams[i].Weight != u.Weight {
			return false
		}
	}
	return true
}

func (s *UpstreamSelector) initWeightedRoundRobin(upstreams []datasource.WeightedUpstream) {
	s.wrrUpstreams = make([]datasource.WeightedUpstream, len(upstreams))
	copy(s.wrrUpstreams, upstreams)

	s.wrrWeights = make([]int, len(upstreams))
	s.wrrCurrent = make([]int, len(upstreams))

	for i, u := range upstreams {
		s.wrrWeights[i] = u.GetEffectiveWeight()
		s.wrrCurrent[i] = 0
	}
}

// selectIPHash selects upstream based on client IP hash.
func (s *UpstreamSelector) selectIPHash(r *http.Request, upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	ip := getClientIP(r)
	idx := hashToIndex(ip, len(upstreams))
	return &upstreams[idx]
}

// selectURIHash selects upstream based on request URI hash.
func (s *UpstreamSelector) selectURIHash(r *http.Request, upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	idx := hashToIndex(r.URL.Path, len(upstreams))
	return &upstreams[idx]
}

// selectHeaderHash selects upstream based on a specific header hash.
func (s *UpstreamSelector) selectHeaderHash(r *http.Request, upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	headerValue := r.Header.Get(s.hashKey)
	if headerValue == "" {
		// Fallback to weighted random if header is missing
		return s.selectWeightedRandom(upstreams)
	}
	idx := hashToIndex(headerValue, len(upstreams))
	return &upstreams[idx]
}

// selectFirst always selects the first upstream.
func (s *UpstreamSelector) selectFirst(upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	return &upstreams[0]
}

// selectLeastConn selects the upstream with the least active connections.
// Uses local connection tracking. For accurate tracking across requests,
// use IncrementConn when a connection is established and DecrementConn when it closes.
func (s *UpstreamSelector) selectLeastConn(upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	minIdx := 0
	minConns := s.getConnCount(upstreams[0].Address)
	minWeight := upstreams[0].GetEffectiveWeight()

	for i := 1; i < len(upstreams); i++ {
		conns := s.getConnCount(upstreams[i].Address)
		weight := upstreams[i].GetEffectiveWeight()

		// Calculate weighted connection ratio: connections / weight
		// Lower ratio means less loaded (considering capacity)
		// Use multiplication to avoid division: conns * minWeight < minConns * weight
		if conns*minWeight < minConns*weight {
			minIdx = i
			minConns = conns
			minWeight = weight
		} else if conns*minWeight == minConns*weight && weight > minWeight {
			// Tie-breaker: prefer higher weight (more capacity)
			minIdx = i
			minConns = conns
			minWeight = weight
		}
	}

	return &upstreams[minIdx]
}

// getConnCount returns the connection count for an address.
func (s *UpstreamSelector) getConnCount(address string) int {
	if val, ok := s.connCounts.Load(address); ok {
		return int(atomic.LoadInt64(val.(*int64)))
	}
	return 0
}

// IncrementConn increments the connection count for an address.
// Call this when a new connection is established.
func (s *UpstreamSelector) IncrementConn(address string) {
	val, _ := s.connCounts.LoadOrStore(address, new(int64))
	atomic.AddInt64(val.(*int64), 1)
}

// DecrementConn decrements the connection count for an address.
// Call this when a connection is closed.
func (s *UpstreamSelector) DecrementConn(address string) {
	if val, ok := s.connCounts.Load(address); ok {
		atomic.AddInt64(val.(*int64), -1)
	}
}

// selectConsistentHash uses consistent hashing for stable distribution.
func (s *UpstreamSelector) selectConsistentHash(r *http.Request, upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	// Build or update hash ring
	if s.hashRing == nil || !s.hashRing.Matches(upstreams) {
		s.hashRing = NewConsistentHashRing(upstreams, 150) // 150 virtual nodes per upstream
	}

	key := s.hashKey
	if key == "" {
		key = getClientIP(r)
	} else {
		key = r.Header.Get(s.hashKey)
		if key == "" {
			key = getClientIP(r)
		}
	}

	return s.hashRing.Get(key)
}

// selectWeightedRandom selects upstream based on weights randomly.
func (s *UpstreamSelector) selectWeightedRandom(upstreams []datasource.WeightedUpstream) *datasource.WeightedUpstream {
	totalWeight := 0
	for _, u := range upstreams {
		totalWeight += u.GetEffectiveWeight()
	}

	if totalWeight <= 0 {
		//nolint:gosec // non-cryptographic randomness is sufficient for load balancing
		idx := rand.Intn(len(upstreams))
		return &upstreams[idx]
	}

	//nolint:gosec // non-cryptographic randomness is sufficient for load balancing
	target := rand.Intn(totalWeight)
	cumulative := 0

	for i := range upstreams {
		cumulative += upstreams[i].GetEffectiveWeight()
		if target < cumulative {
			return &upstreams[i]
		}
	}

	return &upstreams[0]
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Use RemoteAddr
	addr := r.RemoteAddr
	// Remove port
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

// hashToIndex computes a consistent hash of the input and maps to an index.
// Uses pooled FNV hasher to reduce allocations.
func hashToIndex(input string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnvPool.Get().(hash.Hash32)
	h.Reset()
	h.Write([]byte(input))
	mod := uint64(h.Sum32()) % uint64(n)
	fnvPool.Put(h)
	maxInt := uint64(^uint(0) >> 1)
	if mod > maxInt {
		return 0
	}
	return int(mod)
}

// hashString computes FNV32a hash of a string using pooled hasher.
func hashString(input string) uint32 {
	h := fnvPool.Get().(hash.Hash32)
	h.Reset()
	h.Write([]byte(input))
	result := h.Sum32()
	fnvPool.Put(h)
	return result
}

// ConsistentHashRing implements consistent hashing with virtual nodes.
type ConsistentHashRing struct {
	ring       []hashNode
	upstreams  []datasource.WeightedUpstream
	configHash uint32 // Fast comparison hash
}

type hashNode struct {
	hash     uint32
	upstream *datasource.WeightedUpstream
}

// NewConsistentHashRing creates a new consistent hash ring.
func NewConsistentHashRing(upstreams []datasource.WeightedUpstream, virtualNodes int) *ConsistentHashRing {
	ring := &ConsistentHashRing{
		upstreams: make([]datasource.WeightedUpstream, len(upstreams)),
	}
	copy(ring.upstreams, upstreams)

	// Compute config hash for fast matching
	ring.configHash = computeUpstreamsHash(upstreams)

	// Add virtual nodes for each upstream
	for i := range upstreams {
		weight := upstreams[i].GetEffectiveWeight()
		numNodes := virtualNodes * weight / 10 // Scale by weight
		if numNodes < 1 {
			numNodes = 1
		}

		for j := 0; j < numNodes; j++ {
			key := fmt.Sprintf("%s-%d", upstreams[i].Address, j)
			ring.ring = append(ring.ring, hashNode{
				hash:     hashString(key),
				upstream: &upstreams[i],
			})
		}
	}

	// Sort by hash
	sort.Slice(ring.ring, func(i, j int) bool {
		return ring.ring[i].hash < ring.ring[j].hash
	})

	return ring
}

// Get returns the upstream for the given key.
// Uses pooled FNV hasher to reduce allocations.
func (r *ConsistentHashRing) Get(key string) *datasource.WeightedUpstream {
	if len(r.ring) == 0 {
		return nil
	}

	hash := hashString(key)

	// Binary search for the first node with hash >= key hash
	idx := sort.Search(len(r.ring), func(i int) bool {
		return r.ring[i].hash >= hash
	})

	// Wrap around if necessary
	if idx >= len(r.ring) {
		idx = 0
	}

	return r.ring[idx].upstream
}

// Matches checks if the ring matches the given upstreams using fast hash comparison.
// This is O(1) instead of O(n) by comparing pre-computed config hashes.
func (r *ConsistentHashRing) Matches(upstreams []datasource.WeightedUpstream) bool {
	return r.configHash == computeUpstreamsHash(upstreams)
}

// computeUpstreamsHash computes a hash of the upstreams configuration for fast comparison.
func computeUpstreamsHash(upstreams []datasource.WeightedUpstream) uint32 {
	h := fnvPool.Get().(hash.Hash32)
	h.Reset()
	for _, u := range upstreams {
		h.Write([]byte(u.Address))
		h.Write([]byte{byte(u.Weight), byte(u.Weight >> 8)})
	}
	result := h.Sum32()
	fnvPool.Put(h)
	return result
}
