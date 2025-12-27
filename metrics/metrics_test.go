package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRouteHit(t *testing.T) {
	// Should not panic
	RecordRouteHit("test-key", "backend:8080")
}

func TestRouteMetricsCardinalityMode(t *testing.T) {
	if err := SetRouteMetricsCardinalityFromString("coarse"); err != nil {
		t.Fatalf("SetRouteMetricsCardinalityFromString(coarse) returned error: %v", err)
	}
	if got := RouteMetricsCardinalityMode(); got != RouteMetricsCardinalityCoarse {
		t.Fatalf("expected mode %q, got %q", RouteMetricsCardinalityCoarse, got)
	}

	// Should not panic in coarse mode.
	RecordRouteHit("test-key", "backend:8080")
	RecordRouteMiss("test-key", MissReasonNoConfig)

	if err := SetRouteMetricsCardinalityFromString("detailed"); err != nil {
		t.Fatalf("SetRouteMetricsCardinalityFromString(detailed) returned error: %v", err)
	}
	if got := RouteMetricsCardinalityMode(); got != RouteMetricsCardinalityDetailed {
		t.Fatalf("expected mode %q, got %q", RouteMetricsCardinalityDetailed, got)
	}

	if err := SetRouteMetricsCardinalityFromString("nope"); err == nil {
		t.Fatalf("expected error for invalid mode")
	}
}

func TestRecordRouteMiss(t *testing.T) {
	// Should not panic
	RecordRouteMiss("test-key", MissReasonNoConfig)
	RecordRouteMiss("", MissReasonNoKey)
}

func TestRecordCacheOperations(t *testing.T) {
	// Should not panic
	RecordCacheHit("etcd")
	RecordCacheMiss("redis")
}

func TestRecordRouteConfigParseError(t *testing.T) {
	beforeEtcd := testutil.ToFloat64(RouteConfigParseErrors.WithLabelValues("etcd"))
	beforeUnknown := testutil.ToFloat64(RouteConfigParseErrors.WithLabelValues("unknown"))

	RecordRouteConfigParseError("etcd")
	RecordRouteConfigParseError("")

	afterEtcd := testutil.ToFloat64(RouteConfigParseErrors.WithLabelValues("etcd"))
	afterUnknown := testutil.ToFloat64(RouteConfigParseErrors.WithLabelValues("unknown"))

	if afterEtcd != beforeEtcd+1 {
		t.Fatalf("expected etcd counter to increment by 1 (before=%v after=%v)", beforeEtcd, afterEtcd)
	}
	if afterUnknown != beforeUnknown+1 {
		t.Fatalf("expected unknown counter to increment by 1 (before=%v after=%v)", beforeUnknown, afterUnknown)
	}

	snap := RouteConfigParseErrorSnapshot()
	if snap["etcd"].Total == 0 {
		t.Fatalf("expected snapshot to include etcd with non-zero total")
	}
	if snap["unknown"].Total == 0 {
		t.Fatalf("expected snapshot to include unknown with non-zero total")
	}
}

func TestSetDataSourceHealth(t *testing.T) {
	// Should not panic
	SetDataSourceHealth("etcd", true)
	SetDataSourceHealth("etcd", false)
}

func TestSetRouteConfigCount(t *testing.T) {
	// Should not panic
	SetRouteConfigCount("etcd", 100)
	SetRouteConfigCount("redis", 0)
}

func TestRecordWatchEvent(t *testing.T) {
	// Should not panic
	RecordWatchEvent("etcd", "put")
	RecordWatchEvent("redis", "del")
}

func TestTimer(t *testing.T) {
	timer := NewTimer()
	if timer == nil {
		t.Fatal("NewTimer returned nil")
	}

	time.Sleep(10 * time.Millisecond)

	// Should not panic
	timer.ObserveDuration(RuleMatchLatency)
}

func TestCacheHitRateTracker(t *testing.T) {
	tracker := NewCacheHitRateTracker(time.Minute)

	// Initial rate should be 0
	if rate := tracker.HitRate(); rate != 0.0 {
		t.Errorf("Expected initial hit rate 0.0, got %f", rate)
	}

	// Record some hits and misses
	tracker.RecordHit()
	tracker.RecordHit()
	tracker.RecordHit()
	tracker.RecordMiss()

	// Hit rate should be 0.75
	if rate := tracker.HitRate(); rate != 0.75 {
		t.Errorf("Expected hit rate 0.75, got %f", rate)
	}

	// More misses
	tracker.RecordMiss()
	tracker.RecordMiss()
	tracker.RecordMiss()

	// Hit rate should be 3/7 ≈ 0.4286
	rate := tracker.HitRate()
	expected := 3.0 / 7.0
	if rate < expected-0.01 || rate > expected+0.01 {
		t.Errorf("Expected hit rate ~%f, got %f", expected, rate)
	}
}

func TestCacheHitRateTrackerReset(t *testing.T) {
	// Short window for testing
	tracker := NewCacheHitRateTracker(50 * time.Millisecond)

	tracker.RecordHit()
	tracker.RecordMiss()

	if rate := tracker.HitRate(); rate != 0.5 {
		t.Errorf("Expected hit rate 0.5, got %f", rate)
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	// Record new hit - should reset counters
	tracker.RecordHit()

	// Rate should now be 1.0 (1 hit, 0 misses after reset)
	if rate := tracker.HitRate(); rate != 1.0 {
		t.Errorf("Expected hit rate 1.0 after reset, got %f", rate)
	}
}

func TestMissReasons(t *testing.T) {
	reasons := []MissReason{
		MissReasonNoKey,
		MissReasonNoConfig,
		MissReasonNoMatch,
		MissReasonNotInPool,
		MissReasonUnhealthy,
		MissReasonError,
		MissReasonNoReplacer,
		MissReasonNoDataSource,
		MissReasonNoKeyExtractor,
	}

	for _, reason := range reasons {
		if string(reason) == "" {
			t.Errorf("MissReason should have a non-empty string value")
		}
	}
}
