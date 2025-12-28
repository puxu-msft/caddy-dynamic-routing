// Package metrics provides Prometheus metrics for the dynamic load balancer.
package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "caddy"
	subsystem = "dynamic_lb"
)

// RouteMetricsCardinality controls whether route hit/miss metrics include
// high-cardinality labels (routing key / upstream).
//
// Detailed mode is useful for debugging but can create an unbounded number of
// time series in multi-tenant or high-key environments.
type RouteMetricsCardinality string

const (
	// RouteMetricsCardinalityDetailed includes routing key/upstream labels.
	RouteMetricsCardinalityDetailed RouteMetricsCardinality = "detailed"
	// RouteMetricsCardinalityCoarse collapses routing key/upstream labels to a constant.
	RouteMetricsCardinalityCoarse RouteMetricsCardinality = "coarse"
)

const coarseLabelValue = "__all__"

var routeMetricsCardinality atomic.Int32

func init() {
	// Preserve backwards-compatible behavior: detailed metrics unless explicitly configured.
	routeMetricsCardinality.Store(int32(0))
}

// SetRouteMetricsCardinalityFromString configures route hit/miss metric cardinality.
// Allowed values: "detailed" (default) or "coarse".
func SetRouteMetricsCardinalityFromString(mode string) error {
	switch RouteMetricsCardinality(strings.ToLower(strings.TrimSpace(mode))) {
	case "", RouteMetricsCardinalityDetailed:
		routeMetricsCardinality.Store(int32(0))
		return nil
	case RouteMetricsCardinalityCoarse:
		routeMetricsCardinality.Store(int32(1))
		return nil
	default:
		return fmt.Errorf("invalid metrics cardinality %q (allowed: %q, %q)", mode, RouteMetricsCardinalityDetailed, RouteMetricsCardinalityCoarse)
	}
}

// RouteMetricsCardinalityMode returns the currently configured cardinality mode.
func RouteMetricsCardinalityMode() RouteMetricsCardinality {
	if routeMetricsCardinality.Load() == int32(1) {
		return RouteMetricsCardinalityCoarse
	}
	return RouteMetricsCardinalityDetailed
}

var (
	// RouteHits tracks total number of route hits by key and upstream.
	RouteHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "route_hits_total",
			Help:      "Total number of route hits by key and upstream",
		},
		[]string{"key", "upstream"},
	)

	// RouteMisses tracks total number of route misses by key and reason.
	RouteMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "route_misses_total",
			Help:      "Total number of route misses by key and reason",
		},
		[]string{"key", "reason"},
	)

	// RouteConfigParseErrors tracks the number of route-config parse errors by source type.
	RouteConfigParseErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "route_config_parse_errors_total",
			Help:      "Total number of route configuration parse errors by source type",
		},
		[]string{"source_type"},
	)

	// DataSourceLatency tracks data source lookup latency.
	DataSourceLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "datasource_latency_seconds",
			Help:      "Data source lookup latency in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"source_type"},
	)

	// CacheHits tracks cache hit counts by source type.
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "cache_hits_total",
			Help:      "Total number of cache hits",
		},
		[]string{"source_type"},
	)

	// CacheMisses tracks cache miss counts by source type.
	CacheMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "cache_misses_total",
			Help:      "Total number of cache misses",
		},
		[]string{"source_type"},
	)

	// DataSourceHealth tracks data source health status (1 = healthy, 0 = unhealthy).
	DataSourceHealth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "datasource_healthy",
			Help:      "Data source health status (1 = healthy, 0 = unhealthy)",
		},
		[]string{"source_type"},
	)

	// ActiveUpstreams tracks the number of active upstreams from dynamic routing.
	ActiveUpstreams = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "active_upstreams",
			Help:      "Number of active upstreams from dynamic routing",
		},
		[]string{"source_type"},
	)

	// RouteConfigCount tracks the number of route configurations cached.
	RouteConfigCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "route_configs_total",
			Help:      "Number of route configurations currently cached",
		},
		[]string{"source_type"},
	)

	// RuleMatchLatency tracks rule matching latency.
	RuleMatchLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rule_match_latency_seconds",
			Help:      "Rule matching latency in seconds",
			Buckets:   []float64{.0001, .0005, .001, .005, .01, .05},
		},
	)

	// InstanceFallbacks tracks how often instance-prefer routing falls back to version routing.
	// This is intentionally low-cardinality: reason is one of a small fixed set.
	InstanceFallbacks = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "instance_fallbacks_total",
			Help:      "Total number of instance->version fallbacks by reason",
		},
		[]string{"reason"},
	)

	// WatchEvents tracks watch events by source type and event type.
	WatchEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "watch_events_total",
			Help:      "Total number of watch events received",
		},
		[]string{"source_type", "event_type"},
	)

	// Pool metrics for connection pool monitoring (Redis, etc.)

	// PoolHits tracks the number of times a free connection was found in the pool.
	PoolHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_hits_total",
			Help:      "Number of times a free connection was found in the pool",
		},
		[]string{"source_type", "instance"},
	)

	// PoolMisses tracks the number of times a free connection was NOT found in the pool.
	PoolMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_misses_total",
			Help:      "Number of times a free connection was NOT found in the pool",
		},
		[]string{"source_type", "instance"},
	)

	// PoolTimeouts tracks the number of times a wait timeout occurred.
	PoolTimeouts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_timeouts_total",
			Help:      "Number of times a wait timeout occurred",
		},
		[]string{"source_type", "instance"},
	)

	// PoolTotalConns tracks the current number of total connections in the pool.
	PoolTotalConns = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_total_connections",
			Help:      "Current number of total connections in the pool",
		},
		[]string{"source_type", "instance"},
	)

	// PoolIdleConns tracks the current number of idle connections in the pool.
	PoolIdleConns = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_idle_connections",
			Help:      "Current number of idle connections in the pool",
		},
		[]string{"source_type", "instance"},
	)

	// PoolStaleConns tracks the number of stale connections removed from the pool.
	PoolStaleConns = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_stale_connections_total",
			Help:      "Total number of stale connections removed from the pool",
		},
		[]string{"source_type", "instance"},
	)
)

// RouteConfigParseErrorInfo is a low-cardinality in-process summary of route-config parse errors.
// It is intended for Admin API debugging when Prometheus is not available.
type RouteConfigParseErrorInfo struct {
	Total  uint64    `json:"total"`
	LastAt time.Time `json:"last_at,omitempty"`
}

var routeConfigParseErrorMu sync.Mutex
var routeConfigParseErrorInfoBySourceType = make(map[string]RouteConfigParseErrorInfo)

// MissReason represents the reason for a route miss.
type MissReason string

// MissReason constants enumerate the possible reasons for a route miss.
const (
	MissReasonNoKey               MissReason = "no_key"
	MissReasonNoConfig            MissReason = "no_config"
	MissReasonNoMatch             MissReason = "no_match"
	MissReasonNotInPool           MissReason = "not_in_pool"
	MissReasonUnhealthy           MissReason = "unhealthy"
	MissReasonError               MissReason = "error"
	MissReasonDisabled            MissReason = "disabled"
	MissReasonExpired             MissReason = "expired"
	MissReasonNoReplacer          MissReason = "no_replacer"
	MissReasonNoDataSource        MissReason = "no_datasource"
	MissReasonNoKeyExtractor      MissReason = "no_key_extractor"
	MissReasonNoAvailableUpstream MissReason = "no_available_upstream"
)

// Timer is a helper for timing operations.
type Timer struct {
	start time.Time
}

// NewTimer creates a new timer.
func NewTimer() *Timer {
	return &Timer{start: time.Now()}
}

// ObserveDuration observes the duration since the timer was created.
func (t *Timer) ObserveDuration(observer prometheus.Observer) {
	observer.Observe(time.Since(t.start).Seconds())
}

// RecordRouteHit records a successful route hit.
func RecordRouteHit(key, upstream string) {
	if RouteMetricsCardinalityMode() == RouteMetricsCardinalityCoarse {
		RouteHits.WithLabelValues(coarseLabelValue, coarseLabelValue).Inc()
		return
	}
	RouteHits.WithLabelValues(key, upstream).Inc()
}

// RecordRouteMiss records a route miss with reason.
func RecordRouteMiss(key string, reason MissReason) {
	if RouteMetricsCardinalityMode() == RouteMetricsCardinalityCoarse {
		RouteMisses.WithLabelValues(coarseLabelValue, string(reason)).Inc()
		return
	}
	RouteMisses.WithLabelValues(key, string(reason)).Inc()
}

// RecordInstanceFallback records an instance->version fallback event.
func RecordInstanceFallback(reason MissReason) {
	if reason == "" {
		reason = MissReasonError
	}
	InstanceFallbacks.WithLabelValues(string(reason)).Inc()
}

// RecordRouteConfigParseError records a route-config parse error for a given data source type.
func RecordRouteConfigParseError(sourceType string) {
	if sourceType == "" {
		sourceType = "unknown"
	}
	RouteConfigParseErrors.WithLabelValues(sourceType).Inc()

	now := time.Now()
	routeConfigParseErrorMu.Lock()
	info := routeConfigParseErrorInfoBySourceType[sourceType]
	info.Total++
	info.LastAt = now
	routeConfigParseErrorInfoBySourceType[sourceType] = info
	routeConfigParseErrorMu.Unlock()
}

// RouteConfigParseErrorSnapshot returns a best-effort snapshot of parse error totals by source type.
func RouteConfigParseErrorSnapshot() map[string]RouteConfigParseErrorInfo {
	routeConfigParseErrorMu.Lock()
	defer routeConfigParseErrorMu.Unlock()

	out := make(map[string]RouteConfigParseErrorInfo, len(routeConfigParseErrorInfoBySourceType))
	for k, v := range routeConfigParseErrorInfoBySourceType {
		out[k] = v
	}
	return out
}

// RecordCacheHit records a cache hit.
func RecordCacheHit(sourceType string) {
	CacheHits.WithLabelValues(sourceType).Inc()
}

// RecordCacheMiss records a cache miss.
func RecordCacheMiss(sourceType string) {
	CacheMisses.WithLabelValues(sourceType).Inc()
}

// SetDataSourceHealth sets the data source health status.
func SetDataSourceHealth(sourceType string, healthy bool) {
	val := 0.0
	if healthy {
		val = 1.0
	}
	DataSourceHealth.WithLabelValues(sourceType).Set(val)
}

// SetRouteConfigCount sets the number of cached route configurations.
func SetRouteConfigCount(sourceType string, count int) {
	RouteConfigCount.WithLabelValues(sourceType).Set(float64(count))
}

// RecordWatchEvent records a watch event.
func RecordWatchEvent(sourceType, eventType string) {
	WatchEvents.WithLabelValues(sourceType, eventType).Inc()
}

// CacheHitRateTracker tracks cache hit rate over a sliding window.
type CacheHitRateTracker struct {
	mu        sync.RWMutex
	hits      int64
	misses    int64
	lastReset time.Time
	window    time.Duration
}

// NewCacheHitRateTracker creates a new cache hit rate tracker with the given window.
func NewCacheHitRateTracker(window time.Duration) *CacheHitRateTracker {
	return &CacheHitRateTracker{
		lastReset: time.Now(),
		window:    window,
	}
}

// RecordHit records a cache hit.
func (t *CacheHitRateTracker) RecordHit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeReset()
	t.hits++
}

// RecordMiss records a cache miss.
func (t *CacheHitRateTracker) RecordMiss() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeReset()
	t.misses++
}

// HitRate returns the current cache hit rate (0.0 to 1.0).
func (t *CacheHitRateTracker) HitRate() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	total := t.hits + t.misses
	if total == 0 {
		return 0.0
	}
	return float64(t.hits) / float64(total)
}

// maybeReset resets counters if the window has elapsed.
// Must be called with mu held.
func (t *CacheHitRateTracker) maybeReset() {
	if time.Since(t.lastReset) > t.window {
		t.hits = 0
		t.misses = 0
		t.lastReset = time.Now()
	}
}

// PoolStats represents connection pool statistics.
// This is a generic structure that can be populated from various client libraries.
type PoolStats struct {
	Hits       uint32 // number of times free connection was found in the pool
	Misses     uint32 // number of times free connection was NOT found in the pool
	Timeouts   uint32 // number of times a wait timeout occurred
	TotalConns uint32 // number of total connections in the pool
	IdleConns  uint32 // number of idle connections in the pool
	StaleConns uint32 // number of stale connections removed from the pool
}

// PoolStatsCollector collects and exports pool statistics as Prometheus metrics.
type PoolStatsCollector struct {
	sourceType string
	instance   string

	// Track previous counter values to compute deltas
	mu             sync.Mutex
	prevHits       uint32
	prevMisses     uint32
	prevTimeouts   uint32
	prevStaleConns uint32
}

// NewPoolStatsCollector creates a new pool stats collector.
func NewPoolStatsCollector(sourceType, instance string) *PoolStatsCollector {
	return &PoolStatsCollector{
		sourceType: sourceType,
		instance:   instance,
	}
}

// Update updates the pool metrics with the latest stats.
// For counter metrics (Hits, Misses, Timeouts, StaleConns), it computes and adds the delta.
// For gauge metrics (TotalConns, IdleConns), it sets the current value.
func (c *PoolStatsCollector) Update(stats PoolStats) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Counter metrics: compute delta and add
	if stats.Hits >= c.prevHits {
		delta := stats.Hits - c.prevHits
		if delta > 0 {
			PoolHits.WithLabelValues(c.sourceType, c.instance).Add(float64(delta))
		}
	}
	c.prevHits = stats.Hits

	if stats.Misses >= c.prevMisses {
		delta := stats.Misses - c.prevMisses
		if delta > 0 {
			PoolMisses.WithLabelValues(c.sourceType, c.instance).Add(float64(delta))
		}
	}
	c.prevMisses = stats.Misses

	if stats.Timeouts >= c.prevTimeouts {
		delta := stats.Timeouts - c.prevTimeouts
		if delta > 0 {
			PoolTimeouts.WithLabelValues(c.sourceType, c.instance).Add(float64(delta))
		}
	}
	c.prevTimeouts = stats.Timeouts

	if stats.StaleConns >= c.prevStaleConns {
		delta := stats.StaleConns - c.prevStaleConns
		if delta > 0 {
			PoolStaleConns.WithLabelValues(c.sourceType, c.instance).Add(float64(delta))
		}
	}
	c.prevStaleConns = stats.StaleConns

	// Gauge metrics: set current value
	PoolTotalConns.WithLabelValues(c.sourceType, c.instance).Set(float64(stats.TotalConns))
	PoolIdleConns.WithLabelValues(c.sourceType, c.instance).Set(float64(stats.IdleConns))
}
