# Admin API (Quick Peek)

This example collects a few quick `curl` commands for inspecting the runtime state exposed by this module under `/dynamic-lb/*`.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Related: [../metrics/README.md](../metrics/README.md)
- Related: [../docker/README.md](../docker/README.md)
- Developer guide: [../dev-guide/README.md](../dev-guide/README.md)

## Prerequisites

- Caddy is running with the plugin loaded.
- Caddy Admin API is enabled.
- By default, Caddy’s admin listener is `http://localhost:2019`.

## Inspect runtime state

```bash
curl http://localhost:2019/dynamic-lb/stats
curl http://localhost:2019/dynamic-lb/health
curl http://localhost:2019/dynamic-lb/routes
curl http://localhost:2019/dynamic-lb/policies
curl http://localhost:2019/dynamic-lb/cache
```

## Route-config parse errors (quick check)

Both `/dynamic-lb/health` and `/dynamic-lb/cache` include a low-cardinality summary field:

- `route_config_parse_errors`: map keyed by `source_type` (e.g. `etcd`, `redis`, `consul`, ...)
	- `total`: cumulative parse failures observed in-process
	- `last_at`: last time a parse failure was recorded (RFC3339)

Example (requires `jq`):

```bash
curl -s http://localhost:2019/dynamic-lb/health | jq '.route_config_parse_errors'
curl -s http://localhost:2019/dynamic-lb/cache  | jq '.route_config_parse_errors'
```

## Clear caches

```bash
curl -X DELETE http://localhost:2019/dynamic-lb/cache
```

## If you get 404

- Confirm the Caddy binary includes the module `admin.api.dynamic_lb`.
- Confirm you’re hitting the correct admin listener.
