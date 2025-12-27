# caddy-dynamic-routing

A Caddy module that provides dynamic routing for reverse proxy load balancing. Route requests based on runtime configuration from external data sources.

## Contents

- [What it does](#what-it-does)
- [Installation](#installation)
- [Metrics](#metrics)
- [Quick Start](#quick-start)
- [Admin API](#admin-api)
- [Documentation & examples](#documentation--examples)
- [License](#license)

## What it does

- Pick an upstream dynamically per request based on a routing key (any Caddy placeholder).
- Read route configs from external data sources (etcd/Redis/Consul/file/HTTP/SQL/Kubernetes/Zookeeper/composite).
- Expose Prometheus metrics and a Caddy Admin API surface under `/dynamic-lb/*`.

Note: the event bus (`events.go`) and the `tracing/` package provide helpers/APIs, but the core routing path does not emit events/spans unless you wire them in.

## Installation

See [examples/dev-guide/README.md](examples/dev-guide/README.md).

## Metrics

Key Prometheus metrics (full reference: [examples/metrics/README.md](examples/metrics/README.md)):

- `caddy_dynamic_lb_route_hits_total`
- `caddy_dynamic_lb_route_misses_total`
- `caddy_dynamic_lb_route_config_parse_errors_total`
- `caddy_dynamic_lb_datasource_latency_seconds`

## Quick Start

For a working end-to-end configuration, see:

- [Basic quickstart](examples/basic/README.md)
- [Data source configs](examples/datasources/README.md)

## Admin API

For runtime inspection and cache operations, see:

- [Admin API (quick peek)](examples/admin-api/README.md)

## Documentation & examples

- [Examples entrypoint](examples/README.md)
- [Basic quickstart](examples/basic/README.md)
- [Data source configs](examples/datasources/README.md)
- [Algorithm configs](examples/algorithms/README.md)
- [Composite (multi-source)](examples/composite/README.md)
- [Multi-tenant patterns (includes dynamic_upstreams)](examples/multi-tenant/README.md)
- [Events helper APIs](examples/events/README.md)
- [Tracing helper APIs](examples/tracing/README.md)
- [Metrics reference](examples/metrics/README.md)
- [Design & architecture](DESIGN.md)
- [Contributing / development](CONTRIBUTING.md)
- [Docker environment](examples/docker/README.md)

## License

Apache 2.0
