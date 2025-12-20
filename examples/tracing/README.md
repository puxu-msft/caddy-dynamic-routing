# OpenTelemetry Tracing Examples

Integrate with OpenTelemetry for distributed tracing.

## Span Names

| Span | Description |
|------|-------------|
| `dynamic_lb.route_selection` | Route selection process |
| `dynamic_lb.datasource.get` | Data source query |
| `dynamic_lb.cache.lookup` | Cache lookup |

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `dynamic_lb.routing_key` | string | Routing key |
| `dynamic_lb.upstream` | string | Selected upstream |
| `dynamic_lb.algorithm` | string | Algorithm used |
| `dynamic_lb.cache_hit` | bool | Cache hit/miss |
| `dynamic_lb.fallback_used` | bool | Whether fallback was used |
| `dynamic_lb.fallback_reason` | string | Reason for fallback |
| `dynamic_lb.config_version` | int64 | Config version |
| `dynamic_lb.selection_time_ms` | float64 | Selection time (ms) |
| `dynamic_lb.datasource_type` | string | Data source type |
| `dynamic_lb.healthy` | bool | Health status |

## Integration with Jaeger

```go
package main

import (
    "context"
    "log"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/jaeger"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

func initTracer() func() {
    // Create Jaeger exporter
    exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(
        jaeger.WithEndpoint("http://jaeger:14268/api/traces"),
    ))
    if err != nil {
        log.Fatal(err)
    }

    // Create tracer provider
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String("caddy-dynamic-routing"),
            semconv.ServiceVersionKey.String("1.0.0"),
        )),
    )

    otel.SetTracerProvider(tp)

    return func() {
        if err := tp.Shutdown(context.Background()); err != nil {
            log.Printf("Error shutting down tracer provider: %v", err)
        }
    }
}

func main() {
    cleanup := initTracer()
    defer cleanup()

    // Start your application...
}
```

## Integration with OTLP (OpenTelemetry Collector)

```go
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

func initOTLPTracer() func() {
    exporter, err := otlptracegrpc.New(context.Background(),
        otlptracegrpc.WithEndpoint("otel-collector:4317"),
        otlptracegrpc.WithInsecure(),
    )
    if err != nil {
        log.Fatal(err)
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.1)), // 10% sampling
    )

    otel.SetTracerProvider(tp)

    return func() {
        tp.Shutdown(context.Background())
    }
}
```

## Docker Compose with Jaeger

```yaml
version: "3.8"

services:
  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
      - "16686:16686"  # UI
      - "14268:14268"  # Collector HTTP
    environment:
      COLLECTOR_ZIPKIN_HOST_PORT: ":9411"

  caddy:
    build: .
    environment:
      OTEL_EXPORTER_JAEGER_ENDPOINT: "http://jaeger:14268/api/traces"
    depends_on:
      - jaeger
```

## Viewing Traces

1. Open Jaeger UI: http://localhost:16686
2. Select service: `caddy-dynamic-routing`
3. Click "Find Traces"
4. View trace timeline and spans

## Sample Trace

```
dynamic_lb.request (12.5ms)
├── dynamic_lb.route_selection (8.2ms)
│   ├── dynamic_lb.cache.lookup (0.1ms) [cache_hit=false]
│   └── dynamic_lb.datasource.get (7.8ms) [datasource_type=etcd]
```

## Using the Tracer in Code

```go
import (
    "github.com/puxu-msft/caddy-dynamic-routing/tracing"
)

func selectRoute(ctx context.Context, r *http.Request, key string) {
    tracer := tracing.NewTracer(true)

    // Start route selection span
    ctx, span := tracer.StartRouteSelection(ctx, r, key)
    defer tracer.EndSpan(span)

    // Start cache lookup span
    cacheCtx, cacheSpan := tracer.StartCacheLookup(ctx, key)
    config := cache.Get(key)
    tracer.RecordCacheHit(cacheSpan, config != nil)
    tracer.EndSpan(cacheSpan)

    if config == nil {
        // Start data source query span
        dsCtx, dsSpan := tracer.StartDataSourceGet(cacheCtx, "etcd", key)
        config, err = dataSource.Get(dsCtx, key)
        if err != nil {
            tracer.RecordError(dsSpan, err)
        }
        tracer.RecordDataSourceHealth(dsSpan, dataSource.Healthy())
        tracer.EndSpan(dsSpan)
    }

    // Record selection result
    tracer.RecordUpstreamSelected(span, upstream, algorithm, config.Version)
}
```
