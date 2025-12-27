# Multi-Tenant SaaS Example

Complete example for a multi-tenant SaaS application with:
- Per-tenant routing
- A/B testing support
- Traffic shifting
- Fallback handling

Navigation:

- Back to examples index: [../README.md](../README.md)
- Algorithms: [../algorithms/README.md](../algorithms/README.md)
- Data sources: [../datasources/README.md](../datasources/README.md)
- Docker environment: [../docker/README.md](../docker/README.md)

## Architecture

```
                    ┌─────────────────────┐
                    │     Caddy SLB       │
                    │  (Dynamic Routing)  │
                    └─────────┬───────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
        ┌─────▼─────┐   ┌─────▼─────┐   ┌─────▼─────┐
        │ Tenant A  │   │ Tenant B  │   │ Tenant C  │
        │ Backends  │   │ Backends  │   │ Backends  │
        └───────────┘   └───────────┘   └───────────┘
```

## Caddyfile

```caddy
{
    admin :2019
}

api.saas.com {
    reverse_proxy {
        # Dynamic backend discovery
        dynamic_upstreams {
            key {http.request.header.X-Tenant-ID}
            default_key "shared"
            refresh_interval 30s

            etcd {
                endpoints etcd1:2379 etcd2:2379 etcd3:2379
                prefix /saas/upstreams/
            }
        }

        # Dynamic selection policy
        lb_policy dynamic {
            key {http.request.header.X-Tenant-ID}

            etcd {
                endpoints etcd1:2379 etcd2:2379 etcd3:2379
                prefix /saas/routing/
            }

            fallback round_robin
        }

        health_uri /health
        health_interval 10s
    }
}
```

## Tenant Configurations

### Basic Tenant

```bash
# Single backend
etcdctl put /saas/routing/tenant-basic "backend-shared:8080"
```

### Premium Tenant with Dedicated Backends

```bash
etcdctl put /saas/routing/tenant-premium '{
  "rules": [{
    "upstreams": [
      {"address": "backend-premium-1:8080", "weight": 1},
      {"address": "backend-premium-2:8080", "weight": 1}
    ]
  }],
  "algorithm": "round_robin",
  "description": "Premium tenant with dedicated resources"
}'
```

### A/B Testing Configuration

```yaml
# 80% traffic to v1, 20% to v2
rules:
  - match:
      http.request.header.X-AB-Test: "enabled"
    upstreams:
      - address: backend-v1:8080
        weight: 80
      - address: backend-v2:8080
        weight: 20
    algorithm: weighted_random

  - upstreams:
      - address: backend-v1:8080

description: "A/B testing for feature rollout"
```

### Canary Deployment

```yaml
# Route by user ID for consistent experience
rules:
  - match:
      http.request.header.X-Canary: "true"
    upstreams:
      - address: backend-canary:8080
    priority: 10

  - upstreams:
      - address: backend-stable-1:8080
      - address: backend-stable-2:8080
    priority: 0

algorithm: round_robin
```

## Dynamic Upstreams

```bash
# Set available backends for a tenant
etcdctl put /saas/upstreams/tenant-enterprise '{
  "rules": [{
    "upstreams": [
      {"address": "enterprise-1.internal:8080"},
      {"address": "enterprise-2.internal:8080"},
      {"address": "enterprise-3.internal:8080"}
    ]
  }]
}'
```

## Run with Docker

```bash
cd examples/docker
docker compose up --build -d
```

This example does not ship a dedicated Compose stack. Start from the canonical environment: [../docker/README.md](../docker/README.md)

Then adapt the Caddy config and etcd key prefixes from this document to your environment.
