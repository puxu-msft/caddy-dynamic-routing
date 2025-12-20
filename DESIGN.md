# caddy-dynamic-routing Design

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Caddy Server                            │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                    reverse_proxy                          │  │
│  │  ┌─────────────────────────────────────────────────────┐  │  │
│  │  │           DynamicSelection (lb_policy)              │  │  │
│  │  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │  │  │
│  │  │  │  Extractor  │  │   Matcher   │  │  Selector   │  │  │  │
│  │  │  │  (key expr) │  │  (rules)    │  │ (algorithm) │  │  │  │
│  │  │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  │  │  │
│  │  │         │                │                │         │  │  │
│  │  │         └────────────────┼────────────────┘         │  │  │
│  │  │                          │                          │  │  │
│  │  │                   ┌──────▼──────┐                   │  │  │
│  │  │                   │  DataSource │                   │  │  │
│  │  │                   └──────┬──────┘                   │  │  │
│  │  └──────────────────────────┼──────────────────────────┘  │  │
│  └─────────────────────────────┼─────────────────────────────┘  │
└────────────────────────────────┼────────────────────────────────┘
                                 │
     ┌───────────────────────────┼───────────────────────────┐
     │                           │                           │
     ▼                           ▼                           ▼
┌─────────┐               ┌─────────────┐              ┌─────────┐
│  etcd   │               │    Redis    │              │ Consul  │
│         │               │             │              │         │
└─────────┘               └─────────────┘              └─────────┘
```

## Project Structure

```
caddy-dynamic-routing/
├── plugin.go              # DynamicSelection - main module
├── caddyfile.go           # Caddyfile parser
├── admin.go               # Admin API endpoints
├── events.go              # Event subscription system
├── dynamic_upstreams.go   # Dynamic upstream discovery
├── extractor/             # Key extraction from requests
├── matcher/               # Rule matching + selection algorithms
├── datasource/            # Data source interface and implementations
│   ├── types.go           # RouteConfig, DataSource interface
│   ├── cache/             # LRU cache with negative caching
│   ├── etcd/
│   ├── redis/
│   ├── consul/
│   ├── file/
│   ├── http/
│   ├── composite/         # Multi-source combination
│   ├── zookeeper/
│   ├── sql/
│   └── kubernetes/
├── metrics/               # Prometheus metrics
├── tracing/               # OpenTelemetry integration
├── examples/              # Configuration examples
└── docker/                # Docker test environment
```

## Core Components

### DynamicSelection

Implements `reverseproxy.Selector` interface. Main entry point for routing decisions.

```go
type DynamicSelection struct {
    Key         string              // Placeholder expression for routing key
    DataSource  datasource.DataSource
    Fallback    *caddyhttp.WeakString
}

func (d *DynamicSelection) Select(pool reverseproxy.UpstreamPool,
    req *http.Request, w http.ResponseWriter) *reverseproxy.Upstream
```

### DataSource Interface

All data sources implement this interface:

```go
type DataSource interface {
    Get(ctx context.Context, key string) (*RouteConfig, error)
    Healthy() bool
}
```

### RouteConfig

Configuration retrieved from data sources:

```go
type RouteConfig struct {
    Upstream     string      // Simple: single upstream
    Upstreams    []Upstream  // Multiple upstreams with weights
    Rules        []Rule      // Conditional routing rules
    Algorithm    string      // Load balancing algorithm
    AlgorithmKey string      // Key for hash-based algorithms
    Fallback     string      // Fallback policy
    Version      int         // Configuration version
    Enabled      bool        // Enable/disable routing
}
```

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

Concurrent requests for the same key are coalesced into a single data source call:

```go
result, err, _ := d.sfGroup.Do(key, func() (interface{}, error) {
    return d.dataSource.Get(ctx, key)
})
```

### Negative Caching

Cache "key not found" results to avoid repeated lookups:

```go
if cache.IsNegativeCached(key) {
    return nil, nil  // Skip data source call
}
```

### Lock-Free Algorithms

Weighted round-robin uses atomic operations instead of mutex:

```go
index := atomic.AddUint64(&s.counter, 1)
return upstreams[index % total]
```

### Upstream Pool Indexing

O(1) upstream lookup using pre-built index:

```go
upstreamIndex := make(map[string]*reverseproxy.Upstream)
// Build once during Provision, use for every request
```

## Caddy Module Registration

```go
func init() {
    caddy.RegisterModule(&DynamicSelection{})
}

func (*DynamicSelection) CaddyModule() caddy.ModuleInfo {
    return caddy.ModuleInfo{
        ID:  "http.reverse_proxy.selection_policies.dynamic",
        New: func() caddy.Module { return new(DynamicSelection) },
    }
}
```

### Registered Modules

```
http.reverse_proxy.selection_policies.dynamic
http.reverse_proxy.selection_policies.dynamic.sources.etcd
http.reverse_proxy.selection_policies.dynamic.sources.redis
http.reverse_proxy.selection_policies.dynamic.sources.consul
http.reverse_proxy.selection_policies.dynamic.sources.file
http.reverse_proxy.selection_policies.dynamic.sources.http
http.reverse_proxy.selection_policies.dynamic.sources.composite
http.reverse_proxy.selection_policies.dynamic.sources.zookeeper
http.reverse_proxy.selection_policies.dynamic.sources.sql
http.reverse_proxy.selection_policies.dynamic.sources.kubernetes
http.reverse_proxy.upstreams.dynamic
```

## Event System

Publish-subscribe pattern for routing events:

```go
bus := GetEventBus()
bus.Subscribe(EventRouteSelected, func(name string, data interface{}) {
    event := data.(RouteSelectedEvent)
    // Handle event
})
```

Events:
- `dynamic_lb.route_selected`
- `dynamic_lb.route_missed`
- `dynamic_lb.config_updated`
- `dynamic_lb.config_deleted`
- `dynamic_lb.datasource_health_changed`

## Observability

### Prometheus Metrics

```go
var RouteHits = promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "caddy",
        Subsystem: "dynamic_lb",
        Name:      "route_hits_total",
    },
    []string{"key", "upstream"},
)
```

### OpenTelemetry Tracing

```go
ctx, span := tracer.Start(ctx, "dynamic_lb.route_selection")
defer span.End()
span.SetAttributes(
    attribute.String("routing_key", key),
    attribute.String("upstream", upstream),
)
```

### Admin API

- `GET /dynamic-lb/stats` - Routing statistics
- `GET /dynamic-lb/health` - Health check
- `GET /dynamic-lb/routes` - Cached routes
- `DELETE /dynamic-lb/cache?key=X` - Invalidate cache

## Known Limitations

1. **least_conn** uses local counter, not shared across instances
2. **Composite** data source requires JSON configuration (not Caddyfile)
3. **Admin API routes** returns empty list (cache integration pending)
