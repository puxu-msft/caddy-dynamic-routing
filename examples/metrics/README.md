# Metrics Reference

This document lists Prometheus metrics emitted by **caddy-dynamic-routing** and gives practical guidance on label cardinality.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Related: [../metrics-cardinality/README.md](../metrics-cardinality/README.md)
- Related: [../admin-api/README.md](../admin-api/README.md)

## General guidance

- Prefer low-cardinality labels in production.
- Avoid using detailed per-tenant/per-key labels unless you understand the time-series impact.
- If you need to reduce cardinality, use the `metrics_cardinality` option to switch to coarse metrics for route hit/miss.

## Route selection

- `caddy_dynamic_lb_route_hits_total{key,upstream}`
  - In coarse mode, both `key` and `upstream` are collapsed to `__all__`.

- `caddy_dynamic_lb_route_misses_total{key,reason}`
  - In coarse mode, `key` is collapsed to `__all__` (reason is still preserved).
  - `reason` includes (non-exhaustive): `no_key`, `no_config`, `no_match`, `not_in_pool`, `unhealthy`, `error`, `disabled`, `expired`, `no_replacer`, `no_datasource`, `no_key_extractor`.

## Config parsing quality

- `caddy_dynamic_lb_route_config_parse_errors_total{source_type}`
  - Counts failures to parse a route config payload (e.g., invalid JSON/YAML) per data source type.
  - `source_type` is intentionally low-cardinality (e.g., `etcd`, `redis`, `consul`, `http`, `sql`, `file`, `kubernetes`, `zookeeper`).

## Data source cache & health

- `caddy_dynamic_lb_cache_hits_total{source_type}`
- `caddy_dynamic_lb_cache_misses_total{source_type}`
- `caddy_dynamic_lb_datasource_healthy{source_type}`
  - Gauge: 1 for healthy, 0 for unhealthy.
- `caddy_dynamic_lb_route_configs_total{source_type}`
  - Gauge: number of cached route configs.

## Watch / refresh

- `caddy_dynamic_lb_watch_events_total{source_type,event_type}`

## Notes

- Metric names are stable, but labels may evolve; treat dashboards/alerts as versioned alongside the plugin.
