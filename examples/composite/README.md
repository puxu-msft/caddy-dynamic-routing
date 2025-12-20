# Composite Data Sources

Combine multiple data sources for high availability and flexibility.

## Modes

| Mode | Description |
|------|-------------|
| `failover` | Use first healthy source that returns a result |
| `merge` | Merge results from all sources |
| `priority` | Use first source that returns a result (ignores health) |
| `first` | Only use the first source |

## Failover Example

Primary etcd with Redis fallback:

```json
{
  "source": "composite",
  "mode": "failover",
  "timeout": "5s",
  "sources": [
    {
      "source": "etcd",
      "endpoints": ["etcd1:2379", "etcd2:2379"],
      "prefix": "/caddy/routing/"
    },
    {
      "source": "redis",
      "addr": "redis:6379",
      "prefix": "routing:"
    }
  ]
}
```

## Merge Example

Merge default config from file with overrides from etcd:

```yaml
source: composite
mode: merge
parallel: true
sources:
  - source: file
    path: /etc/caddy/routes/defaults/
    format: yaml
  - source: etcd
    endpoints:
      - localhost:2379
    prefix: /caddy/routing/overrides/
```

When merging:
- Later sources override earlier sources
- Rules are appended (not replaced)
- Metadata is merged

## Caddyfile

```caddy
reverse_proxy {
    lb_policy dynamic {
        key {http.request.header.X-Tenant}

        composite {
            mode failover
            timeout 5s

            sources {
                etcd {
                    endpoints etcd1:2379 etcd2:2379
                    prefix /caddy/routing/
                }
                redis {
                    addr redis:6379
                    prefix routing:
                }
            }
        }

        fallback random
    }
}
```

## With Result Caching

Cache combined results to reduce source queries:

```json
{
  "source": "composite",
  "mode": "failover",
  "cache_results": true,
  "cache_ttl": "30s",
  "sources": [...]
}
```
