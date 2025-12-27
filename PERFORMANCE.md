# Performance & Scalability Roadmap

This document tracks work items to make **caddy-dynamic-routing** suitable for high-performance, high-cardinality, long-running production scenarios.

## Goals

- Sustain high QPS with minimal added latency per request.
- Remain stable under high-cardinality routing keys (many tenants/keys).
- Avoid unbounded in-process memory growth.
- Preserve debuggability via low-overhead observability (metrics + admin endpoints).

## Non-goals

- Cross-instance/global coordination guarantees (e.g., globally accurate `least_conn`) beyond what Caddy’s reverse_proxy already provides.
- Persistent audit/event storage (current event bus is in-process only).

## Current architecture (summary)

- Request path: `DynamicSelection.Select()` extracts a routing key from placeholders, fetches route config from a `DataSource`, matches rules, selects an upstream, then maps it into the static upstream pool.
- Data sources typically implement: cache → negative cache → health gate → singleflight → fetch → cache.
- Observability: Prometheus metrics + Admin API `/dynamic-lb/*`.

## Key risks in high-performance deployments

1. **High-cardinality Prometheus labels**
   - `RouteHits`/`RouteMisses` include `key` (and `upstream`) as labels.
   - In multi-tenant / per-user / high-key environments this can create an unbounded number of time series.

2. **Unbounded in-process registries**
   - Admin per-key route hit/miss stats are stored in maps keyed by routing key.
   - RuleMatcher caches (`regexCache`, `selectorCache`) are unbounded.

3. **Health gating prevents serving cached configs**
   - The DataSource interface contract indicates cached data should still be served when unhealthy.
   - If callers short-circuit on `Healthy()`, cached results cannot be returned.

4. **Config semantics not fully enforced in the request path**
   - `enabled` and `ttl` exist in `RouteConfig`, but the selection path may not enforce them.

5. **Parse failures can be silently treated as “simple upstream”**
   - Invalid JSON/YAML can be mistakenly interpreted as a plain upstream address.
   - Prefer alertable signals: `caddy_dynamic_lb_route_config_parse_errors_total{source_type}` (low-cardinality).

## Work items

### P0 — correctness + safety for long-running services

#### P0.1 Serve cached routes even when unhealthy

**Problem:** short-circuiting on `Healthy()` can force fallback even when cached config exists.

**Change:** in `DynamicSelection.Select`, do not return early on `!Healthy()`. Still attempt `Get()` so the DataSource can serve from cache.

**Acceptance criteria:**
- When the data source reports unhealthy but has a cached config for the key, routing uses the cached config.
- Miss reasons remain sensible (unhealthy vs no_config).

#### P0.2 Bound Admin per-key stats memory

**Problem:** per-key hit/miss maps can grow without bound.

**Change:** replace unbounded maps with a bounded structure (LRU/capped map) and/or aggregate-only mode.

**Acceptance criteria:**
- Memory growth from route stats is bounded by configuration/constant.
- Admin `/dynamic-lb/stats` remains fast and safe for large keyspaces.

### P1 — observability without cardinality explosions

#### P1.1 Add low-cardinality metrics mode

**Problem:** per-key and per-upstream labels are high cardinality.

**Change:** add a configuration option to disable detailed labels and emit coarse metrics instead.

**Acceptance criteria:**
- Coarse mode produces bounded time series.
- Detailed mode remains available for debugging.

### P2 — follow-ups

- Add bounds/TTL to RuleMatcher caches. (done: bounded LRU)
- Make parse failures visible (log/metrics) rather than silently falling back. (done: parse errors are explicit + `caddy_dynamic_lb_route_config_parse_errors_total{source_type}`)
- Optionally enforce `enabled` / `ttl` semantics. (done: request path checks enabled/ttl)

## Verification

For each work item:

- Run `go test ./...`
- Run golangci-lint v2 (`$(go env GOPATH)/bin/golangci-lint run ./...`)
