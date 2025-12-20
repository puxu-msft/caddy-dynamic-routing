package tracing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewTracer_Disabled(t *testing.T) {
	tracer := NewTracer(false)
	if tracer.IsEnabled() {
		t.Error("expected tracer to be disabled")
	}
}

func TestNewTracer_Enabled(t *testing.T) {
	tracer := NewTracer(true)
	if !tracer.IsEnabled() {
		t.Error("expected tracer to be enabled")
	}
}

func TestTracer_StartRouteSelection_Disabled(t *testing.T) {
	tracer := NewTracer(false)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	ctx, span := tracer.StartRouteSelection(context.Background(), req, "test-key")
	defer tracer.EndSpan(span)

	if ctx == nil {
		t.Error("expected non-nil context")
	}
	if span == nil {
		t.Error("expected non-nil span")
	}
}

func TestTracer_StartRouteSelection_Enabled(t *testing.T) {
	tracer := NewTracer(true)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	ctx, span := tracer.StartRouteSelection(context.Background(), req, "test-key")
	defer tracer.EndSpan(span)

	if ctx == nil {
		t.Error("expected non-nil context")
	}
	if span == nil {
		t.Error("expected non-nil span")
	}
}

func TestTracer_StartDataSourceGet(t *testing.T) {
	tracer := NewTracer(true)

	ctx, span := tracer.StartDataSourceGet(context.Background(), "etcd", "test-key")
	defer tracer.EndSpan(span)

	if ctx == nil {
		t.Error("expected non-nil context")
	}
	if span == nil {
		t.Error("expected non-nil span")
	}
}

func TestTracer_StartCacheLookup(t *testing.T) {
	tracer := NewTracer(true)

	ctx, span := tracer.StartCacheLookup(context.Background(), "test-key")
	defer tracer.EndSpan(span)

	if ctx == nil {
		t.Error("expected non-nil context")
	}
	if span == nil {
		t.Error("expected non-nil span")
	}
}

func TestTracer_RecordMethods_Disabled(t *testing.T) {
	tracer := NewTracer(false)
	_, span := tracer.StartRouteSelection(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), "key")
	defer tracer.EndSpan(span)

	// These should not panic when disabled
	tracer.RecordCacheHit(span, true)
	tracer.RecordUpstreamSelected(span, "backend:8080", "round_robin", 1)
	tracer.RecordFallback(span, "no_config")
	tracer.RecordDataSourceHealth(span, true)
	tracer.RecordConfigDetails(span, 2, 3)
	tracer.RecordSelectionTime(span, 100*time.Millisecond)
	tracer.RecordError(span, errors.New("test error"))
}

func TestTracer_RecordMethods_Enabled(t *testing.T) {
	tracer := NewTracer(true)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, span := tracer.StartRouteSelection(context.Background(), req, "key")
	defer tracer.EndSpan(span)

	// These should not panic when enabled
	tracer.RecordCacheHit(span, true)
	tracer.RecordUpstreamSelected(span, "backend:8080", "round_robin", 1)
	tracer.RecordFallback(span, "no_config")
	tracer.RecordDataSourceHealth(span, true)
	tracer.RecordConfigDetails(span, 2, 3)
	tracer.RecordSelectionTime(span, 100*time.Millisecond)
	tracer.RecordError(span, errors.New("test error"))
}

func TestTracingMiddleware_Disabled(t *testing.T) {
	tracer := NewTracer(false)
	middleware := NewTracingMiddleware(tracer)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := middleware.Wrap(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called")
	}
}

func TestTracingMiddleware_Enabled(t *testing.T) {
	tracer := NewTracer(true)
	middleware := NewTracingMiddleware(tracer)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := middleware.Wrap(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called")
	}
}

func TestConstants(t *testing.T) {
	// Verify constants are defined
	if TracerName == "" {
		t.Error("TracerName should not be empty")
	}
	if SpanNameRouteSelection == "" {
		t.Error("SpanNameRouteSelection should not be empty")
	}
	if SpanNameDataSourceGet == "" {
		t.Error("SpanNameDataSourceGet should not be empty")
	}
	if SpanNameCacheLookup == "" {
		t.Error("SpanNameCacheLookup should not be empty")
	}
}

func TestAttributeConstants(t *testing.T) {
	// Verify attribute constants are defined
	attrs := []string{
		AttrRoutingKey,
		AttrUpstream,
		AttrAlgorithm,
		AttrDataSourceType,
		AttrCacheHit,
		AttrFallbackUsed,
		AttrFallbackReason,
		AttrConfigVersion,
		AttrRequestMethod,
		AttrRequestPath,
		AttrHealthy,
		AttrRulesCount,
		AttrUpstreamsCount,
		AttrSelectionTimeMs,
	}

	for _, attr := range attrs {
		if attr == "" {
			t.Error("attribute constant should not be empty")
		}
	}
}

func TestSpanFromContext(t *testing.T) {
	tracer := NewTracer(true)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	ctx, span := tracer.StartRouteSelection(context.Background(), req, "key")
	defer tracer.EndSpan(span)

	extracted := SpanFromContext(ctx)
	if extracted == nil {
		t.Error("expected non-nil span from context")
	}
}

func TestContextWithSpan(t *testing.T) {
	tracer := NewTracer(true)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	_, span := tracer.StartRouteSelection(context.Background(), req, "key")
	defer tracer.EndSpan(span)

	newCtx := ContextWithSpan(context.Background(), span)
	extracted := SpanFromContext(newCtx)
	if extracted == nil {
		t.Error("expected non-nil span from new context")
	}
}
