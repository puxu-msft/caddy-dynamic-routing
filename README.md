# caddy-dynamic-routing

A Caddy module that provides dynamic routing for reverse proxy load balancing. Route requests based on runtime configuration from external data sources.

## Features

- **Dynamic Routing**: Route requests based on headers, cookies, query parameters
- **9 Data Sources**: etcd, Redis, Consul, File, HTTP, Zookeeper, SQL, Kubernetes, Composite
- **9 Load Balancing Algorithms**: round_robin, weighted_round_robin, weighted_random, ip_hash, uri_hash, header_hash, consistent_hash, first, least_conn
- **Real-time Updates**: Watch mechanisms for instant configuration changes
- **Observability**: Prometheus metrics, OpenTelemetry tracing, Admin API
- **High Availability**: Multi-source failover, graceful degradation, local caching

## Installation

```bash
xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing
```

## Quick Start

### Caddyfile

```caddy
example.com {
    reverse_proxy backend1:8080 backend2:8080 backend3:8080 {
        lb_policy dynamic {
            key {http.request.header.X-Tenant}

            etcd {
                endpoints localhost:2379
                prefix /caddy/routing/
            }

            fallback random
        }
    }
}
```

### Set Routes

```bash
# Simple: direct upstream
etcdctl put /caddy/routing/tenant-a "backend-a:8080"

# JSON: with rules and weights
etcdctl put /caddy/routing/tenant-b '{
  "rules": [
    {
      "match": {"http.request.header.X-Version": "v2"},
      "upstreams": [{"address": "backend-v2:8080", "weight": 80}],
      "priority": 10
    }
  ],
  "fallback": "round_robin"
}'
```

## Key Expressions

| Expression | Description |
|------------|-------------|
| `{http.request.header.X-Tenant}` | Request header |
| `{header.X-Tenant}` | Header shortcut |
| `{cookie.session}` | Cookie value |
| `{query.tenant}` | Query parameter |
| `{header.X-Org}-{cookie.region}` | Composite key |

## Data Sources

### etcd

```caddy
etcd {
    endpoints localhost:2379
    prefix /caddy/routing/
    dial_timeout 5s
}
```

### Redis

```caddy
redis {
    addresses localhost:6379
    prefix caddy:routing:
    cluster              # Enable cluster mode
    keyspace_notify      # Use keyspace notifications
}
```

### Consul

```caddy
consul {
    address localhost:8500
    prefix caddy/routing/
    token my-acl-token
}
```

### File

```caddy
file {
    path /etc/caddy/routes/
    format yaml
    watch true
}
```

### HTTP

```caddy
http {
    url http://config-service/routes
    cache_ttl 1m
    refresh_interval 30s
}
```

### SQL

```caddy
sql {
    driver mysql
    dsn "user:password@tcp(localhost:3306)/caddy"
    table routing_configs
    key_column route_key
    value_column config
}
```

### Kubernetes

```caddy
kubernetes {
    namespace default
    configmap_name caddy-routes
}
```

### Zookeeper

```caddy
zookeeper {
    servers localhost:2181
    prefix /caddy/routing/
}
```

### Composite (Multi-Source Failover)

```caddy
composite {
    mode failover
    sources {
        etcd { endpoints etcd:2379 }
        redis { addresses redis:6379 }
    }
}
```

## Load Balancing Algorithms

| Algorithm | Description |
|-----------|-------------|
| `round_robin` | Sequential distribution |
| `weighted_round_robin` | Sequential with weights |
| `weighted_random` | Random with weights (default) |
| `ip_hash` | Consistent by client IP |
| `uri_hash` | Consistent by URI |
| `header_hash` | Consistent by header value |
| `consistent_hash` | Virtual node hashing |
| `first` | Always first available |
| `least_conn` | Least connections |

## Route Configuration

### Simple Format

```bash
etcdctl put /caddy/routing/key "backend:8080"
```

### JSON Format

```json
{
  "rules": [
    {
      "match": {"http.request.header.X-Version": "v2"},
      "upstreams": [
        {"address": "backend-v2:8080", "weight": 80},
        {"address": "backend-v2-backup:8080", "weight": 20}
      ],
      "priority": 10
    }
  ],
  "fallback": "round_robin",
  "algorithm": "weighted_random"
}
```

### YAML Format

```yaml
rules:
  - match:
      http.request.header.X-Version: v2
    upstreams:
      - address: backend-v2:8080
        weight: 80
    priority: 10
fallback: round_robin
```

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `caddy_dynamic_lb_route_hits_total` | Counter | Route hits |
| `caddy_dynamic_lb_route_misses_total` | Counter | Route misses |
| `caddy_dynamic_lb_datasource_latency_seconds` | Histogram | Query latency |
| `caddy_dynamic_lb_cache_hits_total` | Counter | Cache hits |
| `caddy_dynamic_lb_datasource_healthy` | Gauge | Health status |

## Admin API

```bash
curl http://localhost:2019/dynamic-lb/stats
curl http://localhost:2019/dynamic-lb/health
curl http://localhost:2019/dynamic-lb/routes
```

## Dynamic Upstreams

```caddy
reverse_proxy {
    dynamic_upstreams {
        key {http.request.header.X-Tenant}
        refresh_interval 30s
        etcd {
            endpoints localhost:2379
            prefix /caddy/upstreams/
        }
    }
}
```

## Development

```bash
# Build
xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing=./

# Test
go test ./...

# Docker test environment
cd docker && docker compose up -d
```

## Documentation

- [Design & Architecture](DESIGN.md)
- [Contributing Guide](CONTRIBUTING.md)
- [Examples](examples/)

## License

Apache 2.0
