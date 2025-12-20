// Package tracing provides OpenTelemetry tracing support for the dynamic load balancer.
package tracing

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	// TracerName is the name of the tracer used by this package.
	TracerName = "github.com/puxu-msft/caddy-dynamic-routing"

	// SpanNameRouteSelection is the span name for route selection.
	SpanNameRouteSelection = "dynamic_lb.route_selection"

	// SpanNameDataSourceGet is the span name for data source get operations.
	SpanNameDataSourceGet = "dynamic_lb.datasource.get"

	// SpanNameCacheLookup is the span name for cache lookup operations.
	SpanNameCacheLookup = "dynamic_lb.cache.lookup"
)

// Attribute keys for tracing.
const (
	AttrRoutingKey      = "dynamic_lb.routing_key"
	AttrUpstream        = "dynamic_lb.upstream"
	AttrAlgorithm       = "dynamic_lb.algorithm"
	AttrDataSourceType  = "dynamic_lb.datasource_type"
	AttrCacheHit        = "dynamic_lb.cache_hit"
	AttrFallbackUsed    = "dynamic_lb.fallback_used"
	AttrFallbackReason  = "dynamic_lb.fallback_reason"
	AttrConfigVersion   = "dynamic_lb.config_version"
	AttrRequestMethod   = "http.request.method"
	AttrRequestPath     = "http.request.path"
	AttrHealthy         = "dynamic_lb.healthy"
	AttrRulesCount      = "dynamic_lb.rules_count"
	AttrUpstreamsCount  = "dynamic_lb.upstreams_count"
	AttrSelectionTimeMs = "dynamic_lb.selection_time_ms"
)

// Tracer wraps the OpenTelemetry tracer with convenience methods.
type Tracer struct {
	tracer  trace.Tracer
	enabled bool
}

// NewTracer creates a new Tracer.
// If enabled is false, all operations use a noop tracer.
func NewTracer(enabled bool) *Tracer {
	var tracer trace.Tracer
	if enabled {
		tracer = otel.Tracer(TracerName)
	} else {
		tracer = noop.NewTracerProvider().Tracer(TracerName)
	}
	return &Tracer{
		tracer:  tracer,
		enabled: enabled,
	}
}

// IsEnabled returns whether tracing is enabled.
func (t *Tracer) IsEnabled() bool {
	return t.enabled
}

// StartRouteSelection starts a span for route selection.
func (t *Tracer) StartRouteSelection(ctx context.Context, r *http.Request, routingKey string) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, SpanNameRouteSelection,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(AttrRoutingKey, routingKey),
			attribute.String(AttrRequestMethod, r.Method),
			attribute.String(AttrRequestPath, r.URL.Path),
		),
	)
}

// StartDataSourceGet starts a span for data source get operation.
func (t *Tracer) StartDataSourceGet(ctx context.Context, sourceType, key string) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, SpanNameDataSourceGet,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String(AttrDataSourceType, sourceType),
			attribute.String(AttrRoutingKey, key),
		),
	)
}

// StartCacheLookup starts a span for cache lookup operation.
func (t *Tracer) StartCacheLookup(ctx context.Context, key string) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, SpanNameCacheLookup,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(AttrRoutingKey, key),
		),
	)
}

// RecordCacheHit records a cache hit on the span.
func (t *Tracer) RecordCacheHit(span trace.Span, hit bool) {
	span.SetAttributes(attribute.Bool(AttrCacheHit, hit))
}

// RecordUpstreamSelected records the selected upstream on the span.
func (t *Tracer) RecordUpstreamSelected(span trace.Span, upstream, algorithm string, configVersion int64) {
	span.SetAttributes(
		attribute.String(AttrUpstream, upstream),
		attribute.String(AttrAlgorithm, algorithm),
		attribute.Int64(AttrConfigVersion, configVersion),
	)
}

// RecordFallback records that fallback was used.
func (t *Tracer) RecordFallback(span trace.Span, reason string) {
	span.SetAttributes(
		attribute.Bool(AttrFallbackUsed, true),
		attribute.String(AttrFallbackReason, reason),
	)
}

// RecordDataSourceHealth records data source health status.
func (t *Tracer) RecordDataSourceHealth(span trace.Span, healthy bool) {
	span.SetAttributes(attribute.Bool(AttrHealthy, healthy))
}

// RecordConfigDetails records route configuration details.
func (t *Tracer) RecordConfigDetails(span trace.Span, rulesCount, upstreamsCount int) {
	span.SetAttributes(
		attribute.Int(AttrRulesCount, rulesCount),
		attribute.Int(AttrUpstreamsCount, upstreamsCount),
	)
}

// RecordSelectionTime records the time taken for route selection.
func (t *Tracer) RecordSelectionTime(span trace.Span, duration time.Duration) {
	span.SetAttributes(attribute.Float64(AttrSelectionTimeMs, float64(duration.Microseconds())/1000.0))
}

// RecordError records an error on the span.
func (t *Tracer) RecordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// EndSpan ends the span.
func (t *Tracer) EndSpan(span trace.Span) {
	span.End()
}

// TracingMiddleware provides HTTP middleware for tracing.
type TracingMiddleware struct {
	tracer *Tracer
}

// NewTracingMiddleware creates a new TracingMiddleware.
func NewTracingMiddleware(tracer *Tracer) *TracingMiddleware {
	return &TracingMiddleware{tracer: tracer}
}

// Wrap wraps an HTTP handler with tracing.
func (m *TracingMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := m.tracer.tracer.Start(r.Context(), "dynamic_lb.request",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String(AttrRequestMethod, r.Method),
				attribute.String(AttrRequestPath, r.URL.Path),
			),
		)
		defer span.End()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SpanFromContext extracts the span from the context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// ContextWithSpan returns a new context with the span attached.
func ContextWithSpan(ctx context.Context, span trace.Span) context.Context {
	return trace.ContextWithSpan(ctx, span)
}
