# caddy-dynamic-routing Design

## Contents

- [Architecture](#architecture)
- [Project Structure](#project-structure)
- [Core Components](#core-components)
- [Performance Optimizations](#performance-optimizations)
- [Caddy Module Registration](#caddy-module-registration)
- [Event System](#event-system)
- [Observability](#observability)
- [Known Limitations](#known-limitations)

## Architecture

- Caddy `reverse_proxy` delegates upstream selection to `DynamicSelection` (this module).
- `DynamicSelection` extracts a routing key, matches rules (optional), selects an upstream, and consults a `DataSource` for per-key routing configuration.
- Data sources may be backed by external stores (etcd/Redis/Consul/file/HTTP/SQL/Kubernetes/Zookeeper/composite).

## Project Structure

- `plugin.go`: `DynamicSelection` (main module)
- `caddyfile.go`: Caddyfile parsing
- `admin.go`: Admin API endpoints (`/dynamic-lb/*`)
- `events.go`: event bus + event types (optional wiring)
- `extractor/`: routing key extraction
- `matcher/`: rule matching + selection algorithms
- `datasource/`: data source interface and implementations
- `metrics/`: Prometheus metrics
- `tracing/`: OpenTelemetry helpers
- `examples/`: runnable configs and operational docs
- `docker/`: docker-based test environment

## Core Components

### DynamicSelection

Implements `reverseproxy.Selector` interface. Main entry point for routing decisions.

See [plugin.go](plugin.go) for the exact struct fields and `Select(...)` signature.

### DataSource Interface

All data sources implement this interface:

See [datasource/types.go](datasource/types.go).

### RouteConfig

Configuration retrieved from data sources:

See [datasource/types.go](datasource/types.go).

### Selection Algorithms

9 algorithms available:

| Algorithm | Implementation |
|-----------|----------------|
| `round_robin` | Sequential index with atomic counter |
| `weighted_round_robin` | Lock-free weighted selection |
| `weighted_random` | Probability-based random selection |
| `ip_hash` | FNV hash of client IP |
| `uri_hash` | FNV hash of request URI |
| `header_hash` | FNV hash of header value |
| `consistent_hash` | Virtual node ring with binary search |
| `first` | Always returns first healthy upstream |
| `least_conn` | Local connection counter |

## Performance Optimizations

### Request Coalescing (Singleflight)

Concurrent requests for the same key are coalesced into a single data source call (see data source implementations under `datasource/`).

### Negative Caching

Cache "key not found" results to avoid repeated lookups (see `datasource/cache/cache.go`).

Cache implementation details: see [datasource/cache/cache.go](datasource/cache/cache.go).

### Initial Load Timeout

Each data source performs an initial load during `Provision`. The initial load is bound to the module
lifecycle context, and is additionally capped by an optional per-source `initial_load_timeout`.
This prevents slow backends from blocking startup indefinitely while still allowing background
watchers/pollers to retry.

### Lock-Free Algorithms

Some algorithms use atomic operations instead of mutexes (see `matcher/selector.go`).

Selector implementation: see [matcher/selector.go](matcher/selector.go).

### Upstream Pool Indexing

O(1) upstream lookup using pre-built index:

This is implemented in the selection policy; see `plugin.go`.

## Caddy Module Registration

Each module registers itself in its package init; see `plugin.go` and each `datasource/*/*.go`.

Entry module: see [plugin.go](plugin.go).

### Registered Modules

Module IDs are defined in code; see `plugin.go`, `dynamic_upstreams.go`, and the individual data source packages under `datasource/`.

Related files:
- [plugin.go](plugin.go)
- [dynamic_upstreams.go](dynamic_upstreams.go)

## Event System

Publish-subscribe pattern for routing events:

See `events.go`.

Event bus and types: see [events.go](events.go).

Events:
- `dynamic_lb.route_selected`
- `dynamic_lb.route_missed`
- `dynamic_lb.config_updated`
- `dynamic_lb.config_deleted`
- `dynamic_lb.datasource_health_changed`

## Observability

### Prometheus Metrics

See `metrics/metrics.go` and the metrics reference in `examples/metrics/README.md`.

References:
- [metrics/metrics.go](metrics/metrics.go)
- [examples/metrics/README.md](examples/metrics/README.md)

Additional low-cardinality metrics are provided for alerting and operational safety, including:

- `caddy_dynamic_lb_route_config_parse_errors_total{source_type}`: counts route-config parse failures per data source type.

### OpenTelemetry Tracing

See `tracing/` and `examples/tracing/README.md`.

References:
- [tracing/](tracing/)
- [examples/tracing/README.md](examples/tracing/README.md)

### Admin API

- `GET /dynamic-lb/stats` - Routing statistics
- `GET /dynamic-lb/health` - Health check
- `GET /dynamic-lb/routes` - Cached routes
- `GET /dynamic-lb/policies` - Registered dynamic selection policy instances
- `GET /dynamic-lb/cache` - Cache statistics (aggregated + per data source instance)
- `DELETE /dynamic-lb/cache` - Clear caches for all registered data source instances

## Known Limitations

1. **least_conn** uses local counter, not shared across instances
2. **Composite** data source requires JSON configuration (not Caddyfile)
