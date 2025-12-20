# Examples

Configuration examples for caddy-dynamic-routing.

## Directories

| Directory | Description |
|-----------|-------------|
| [basic/](basic/) | Quick start with Docker Compose and etcd |
| [algorithms/](algorithms/) | Load balancing algorithm configurations |
| [datasources/](datasources/) | Configuration for each data source |
| [composite/](composite/) | Multi-source failover and merge |
| [multi-tenant/](multi-tenant/) | SaaS multi-tenant routing patterns |
| [events/](events/) | Event subscription and monitoring |
| [tracing/](tracing/) | OpenTelemetry integration |

## Quick Start

```bash
# Run the basic example with Docker
cd basic
docker compose up -d

# Test routing
curl -H "X-Tenant: tenant-a" http://localhost:8080
```

## Files

- `Caddyfile.example` - Example Caddyfile configuration
- `config.json.example` - Example JSON configuration
- `etcd-setup.sh` - Helper script for etcd route setup
