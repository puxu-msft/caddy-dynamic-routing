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
| [admin-api/](admin-api/) | Admin API endpoints and cache ops |
| [dev-guide/](dev-guide/) | Developer commands and common checks |
| [docker/](docker/) | Docker-based test environment |
| [dev-debug/](dev-debug/) | Enabling debug logs for troubleshooting |
| [metrics-cardinality/](metrics-cardinality/) | Reducing Prometheus time-series cardinality |
| [metrics/](metrics/) | Metrics reference (names, labels, cardinality notes) |

## Files

- `Caddyfile.example` - Example Caddyfile configuration
- `config.json.example` - Example JSON configuration
- `etcd-setup.sh` - Helper script for etcd route setup
