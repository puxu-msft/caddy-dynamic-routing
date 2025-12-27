# Basic Usage Example

This example demonstrates basic dynamic routing with etcd as the data source.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Related: [../docker/README.md](../docker/README.md)
- Next: [../datasources/README.md](../datasources/README.md)
- Admin API: [../admin-api/README.md](../admin-api/README.md)

## Caddyfile

```caddy
{
    admin :2019
}

example.com {
    reverse_proxy backend1:8080 backend2:8080 backend3:8080 {
        lb_policy dynamic {
            # Extract routing key from X-Tenant header
            key {http.request.header.X-Tenant}

            # Use etcd as data source
            etcd {
                endpoints localhost:2379
                prefix /caddy/routing/
            }

            # Fallback to random selection when no match
            fallback random
        }

        health_uri /health
        health_interval 10s
    }
}
```

## Setting Up Routes

### Simple routing (direct upstream)

```bash
# Route tenant-a to backend-a:8080
etcdctl put /caddy/routing/tenant-a "backend-a:8080"

# Route tenant-b to backend-b:8080
etcdctl put /caddy/routing/tenant-b "backend-b:8080"
```

### JSON format with options

```bash
etcdctl put /caddy/routing/tenant-c '{
  "upstream": "backend-c:8080",
  "algorithm": "round_robin",
  "fallback": "random",
  "description": "Tenant C production config"
}'
```

## Testing

```bash
# Request routed to backend-a:8080
curl -H "X-Tenant: tenant-a" http://example.com/api/users

# Request routed to backend-b:8080
curl -H "X-Tenant: tenant-b" http://example.com/api/users

# No tenant header - uses fallback (random)
curl http://example.com/api/users
```

## Docker Compose

This example does not ship a dedicated Docker Compose stack.

Use the canonical Docker environment instead: [../docker/README.md](../docker/README.md)
