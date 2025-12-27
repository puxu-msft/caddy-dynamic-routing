# Claude Instructions - caddy-dynamic-routing

This document contains comprehensive technical understanding and development guidelines for the project, for Claude to reference when assisting with development.

## Quick Navigation

- [Document Positioning](#document-positioning-important)
- [Project Overview](#project-overview)
- [Build and Test](#build-and-test)
- [Core Interfaces](#core-interfaces)
- [Performance Optimizations](#performance-optimizations)
- [Coding Guidelines](#coding-guidelines)
- [Testing Guidelines](#testing-guidelines)
- [User Preferences](#user-preferences)
- [Module IDs](#module-ids)
- [Prometheus Metrics](#prometheus-metrics)

Common entry points:

- Examples index: [examples/README.md](examples/README.md)
- Dev guide (includes install/build/check commands): [examples/dev-guide/README.md](examples/dev-guide/README.md)
- Metrics reference: [examples/metrics/README.md](examples/metrics/README.md)
- Admin API quick peek: [examples/admin-api/README.md](examples/admin-api/README.md)

## Document Positioning (Important)

- **CLAUDE.md (Chinese) is the source of truth**: it should contain the most complete project understanding, user requirements, and collaboration preferences. When there is any conflict, CLAUDE.md wins.
- **CLAUDE_EN.md (English) is a synchronized translation**: intended for English readers/external collaboration. It must stay aligned with CLAUDE.md (minor wording differences are fine; missing constraints/behaviors are not).
- **Split responsibilities across docs**: README is a concise user quickstart; CONTRIBUTING is an exhaustive developer guide; DESIGN focuses on architecture/design decisions. This document does not replace them, but summarizes the non-negotiable constraints.

## Project Overview

**caddy-dynamic-routing** is a Caddy module that provides dynamic routing capabilities for reverse proxy. It routes requests based on runtime configuration from external data sources (etcd, Redis, Consul, etc.).

### Core Value

Unlike static Caddy configuration, this module supports:
- **Runtime Routing Changes**: Update routing configuration without restarting Caddy
- **Multi-tenant Routing**: Route traffic by tenant/customer in SaaS applications
- **A/B Testing and Canary Releases**: Distribute traffic to different backends based on conditions
- **Multi-datacenter Failover**: Combine multiple data sources for high availability

### Project Structure

- `plugin.go`: DynamicSelection main module (implements `reverseproxy.Selector`)
- `caddyfile.go`: Caddyfile parsing
- `admin.go`: Admin API (`/dynamic-lb/*`)
- `extractor/`: key extraction
   - `extractor/extractor.go`: placeholder expression parsing (e.g. `{header.X-Tenant}`)
- `matcher/`: rule matching + selection algorithms
   - `matcher/matcher.go`: condition matching logic (regex, etc.)
   - `matcher/selector.go`: load-balancing algorithm selection
- `datasource/`: data source interface + implementations
   - `datasource/types.go`: core types and interfaces (`RouteConfig`, `DataSource`, etc.)
   - `datasource/cache/`: LRU / negative cache
   - `datasource/etcd/`, `datasource/redis/`, `datasource/consul/`, `datasource/file/`, `datasource/http/`, etc.: data source implementations
- `metrics/`: Prometheus metrics + in-process stats
- `tracing/`: tracing helpers
- `examples/`: all runnable examples/commands/configs
- `docker/`: docker test environment

---

## Build and Test

For commands and toolchain usage, see:

- [examples/dev-guide/README.md](examples/dev-guide/README.md)
   (includes `xcaddy` build/install and common checks)

---

## Core Interfaces

### DataSource Interface

All data sources must implement:

The canonical definition is in code: see [datasource/types.go](datasource/types.go).

### Caddy Module Interfaces

Each module must implement:

| Interface | Method | Description |
|-----------|--------|-------------|
| `caddy.Module` | `CaddyModule()` | Return module ID and constructor |
| `caddy.Provisioner` | `Provision(ctx)` | Initialize, get logger, establish connections |
| `caddy.CleanerUpper` | `Cleanup()` | Release resources, close connections |
| `caddy.Validator` | `Validate()` | Validate configuration |
| `caddyfile.Unmarshaler` | `UnmarshalCaddyfile()` | Parse Caddyfile configuration |

### Interface Guards Pattern

**Must** use compile-time interface checks:

See [examples/dev-guide/code-style.md](examples/dev-guide/code-style.md) for the template.

---

## Performance Optimizations

Implemented optimizations:

| Optimization | Location | Description |
|--------------|----------|-------------|
| **Singleflight** | All data sources | Coalesce concurrent requests for same key, N→1 |
| **Negative Caching** | cache/cache.go | Cache "key not found" to avoid repeated lookups |
| **Lock-free WRR** | matcher/selector.go | Use atomic instead of mutex |
| **FNV Hash Pool** | matcher/selector.go | sync.Pool to reuse hash objects |
| **Upstream Index** | plugin.go | O(1) lookup instead of O(n) traversal |
| **Builder Pool** | matcher/matcher.go | sync.Pool to reuse strings.Builder |

---

## Coding Guidelines

### Adding a New Data Source

1. **Create file**: `datasource/mynewsource/mynewsource.go`

2. **Implementation template**:
   See [examples/dev-guide/adding-datasource.md](examples/dev-guide/adding-datasource.md).

4. **Register module**:
   See [examples/dev-guide/adding-datasource.md](examples/dev-guide/adding-datasource.md).

5. **Test requirements**:
   - `TestMyNewSource_Validate` - Configuration validation
   - `TestMyNewSource_CaddyModule` - Module registration
   - `TestMyNewSource_Get` - Basic functionality
   - `TestMyNewSource_Concurrent` - Concurrency safety (if applicable)

6. **Update documentation**: README.md and DESIGN.md

### Error Handling Guidelines

- Log errors but don't fail requests: `s.logger.Warn("...", zap.Error(err))`
- Set `s.healthy.Store(false)` on connection failure
- Return `nil, nil` when unhealthy, let fallback policy handle it
- Use context timeout for all external calls

### Logging Guidelines

Use `ctx.Logger()` to get structured logger:

Follow the conventions in `examples/dev-guide/code-style.md` and keep logs structured (e.g. include `key`, `source_type`, and the error).

Log levels:
- `Debug`: Detailed debugging information
- `Info`: Normal operation events (config updates, startup complete)
- `Warn`: Recoverable issues (connection failures, parse errors)
- `Error`: Serious issues (should not occur in normal operation)

---

## Testing Guidelines

### Test Types

1. **Unit tests**: Test individual functions/methods
2. **Table-driven tests**: Multiple input scenarios
3. **Concurrent tests**: Thread safety
4. **Integration tests**: Require external services (etcd, Redis, etc.)

### Test Pattern

See [examples/dev-guide/testing.md](examples/dev-guide/testing.md).

### Concurrent Test Pattern

See [examples/dev-guide/testing.md](examples/dev-guide/testing.md).

### Running Tests

See [examples/dev-guide/README.md](examples/dev-guide/README.md) and [examples/dev-guide/testing.md](examples/dev-guide/testing.md).

---

## User Preferences

### Commit Guidelines

- **Do not stage or commit changes** unless explicitly requested
- When committing:
  - Use descriptive commit messages
  - Follow `<type>: <description>` format
  - type: feat, fix, docs, test, refactor, perf, chore

### Code Changes

- **Must** run tests after modifying code
- Update related tests when modifying functionality
- Keep documentation consistent with code
- Prefer editing existing files over creating new ones

### Task Management

- Use TODO tracking for multi-step tasks
- Complete one task before starting the next
- Verify with tests after modifications

### Documentation Responsibilities

| Document | Responsibility | Language |
|----------|----------------|----------|
| README.md | Concise project introduction and quick start | English |
| DESIGN.md | Architecture design and technical details | English |
| CONTRIBUTING.md | Detailed developer guide | English |
| CLAUDE.md | Complete project understanding and dev guidelines | Chinese |
| CLAUDE_EN.md | English version of CLAUDE.md | English |

---

## Module IDs

- `admin.api.dynamic_lb`
- `http.reverse_proxy.selection_policies.dynamic`
- `http.reverse_proxy.selection_policies.dynamic.sources.etcd`
- `http.reverse_proxy.selection_policies.dynamic.sources.redis`
- `http.reverse_proxy.selection_policies.dynamic.sources.consul`
- `http.reverse_proxy.selection_policies.dynamic.sources.file`
- `http.reverse_proxy.selection_policies.dynamic.sources.http`
- `http.reverse_proxy.selection_policies.dynamic.sources.composite`
- `http.reverse_proxy.selection_policies.dynamic.sources.zookeeper`
- `http.reverse_proxy.selection_policies.dynamic.sources.sql`
- `http.reverse_proxy.selection_policies.dynamic.sources.kubernetes`
- `http.reverse_proxy.upstreams.dynamic_source`

---

## Prometheus Metrics

Note: metrics are defined with `namespace=caddy` and `subsystem=dynamic_lb`, so the final Prometheus names are `caddy_dynamic_lb_*`.

For a metrics and labels reference (including cardinality notes), see [examples/metrics/README.md](examples/metrics/README.md).

Important: the repository **defines** a complete set of metrics, but not every metric is currently updated by runtime logic in this version. The metrics that are reliably emitted on the core paths today are mainly:

- `route_hits_total` / `route_misses_total` (DynamicSelection)
- `datasource_latency_seconds` (DynamicSelection and DynamicUpstreams)
- `rule_match_latency_seconds` (DynamicSelection)
- `cache_hits_total` / `cache_misses_total`: currently primarily from `DynamicUpstreams`' upstream-list cache (this is not the same as per-datasource RouteCache hit rate)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `caddy_dynamic_lb_route_hits_total` | Counter | key, upstream | Route hits |
| `caddy_dynamic_lb_route_misses_total` | Counter | key, reason | Route misses |
| `caddy_dynamic_lb_route_config_parse_errors_total` | Counter | source_type | Route-config parse failures per data source type (low cardinality) |
| `caddy_dynamic_lb_datasource_latency_seconds` | Histogram | source_type | Data source query latency |
| `caddy_dynamic_lb_cache_hits_total` | Counter | source_type | Cache hits |
| `caddy_dynamic_lb_cache_misses_total` | Counter | source_type | Cache misses |
| `caddy_dynamic_lb_datasource_healthy` | Gauge | source_type | Data source health |
| `caddy_dynamic_lb_active_upstreams` | Gauge | source_type | Number of active upstreams (dynamic routing) |
| `caddy_dynamic_lb_route_configs_total` | Gauge | source_type | Number of cached route configs |
| `caddy_dynamic_lb_rule_match_latency_seconds` | Histogram | (none) | Rule matching latency |
| `caddy_dynamic_lb_watch_events_total` | Counter | source_type, event_type | Watch events received |
| `caddy_dynamic_lb_pool_hits_total` | Counter | source_type, instance | Connection pool hits |
| `caddy_dynamic_lb_pool_misses_total` | Counter | source_type, instance | Connection pool misses |
| `caddy_dynamic_lb_pool_timeouts_total` | Counter | source_type, instance | Connection pool wait timeouts |
| `caddy_dynamic_lb_pool_total_connections` | Gauge | source_type, instance | Total connections |
| `caddy_dynamic_lb_pool_idle_connections` | Gauge | source_type, instance | Idle connections |
| `caddy_dynamic_lb_pool_stale_connections_total` | Counter | source_type, instance | Stale connections removed |

### Route metric cardinality (metrics_cardinality)

In high-key / multi-tenant environments, the `key` label (and `upstream` for hits) in `route_hits_total` / `route_misses_total` can cause a large number of Prometheus time series.

DynamicSelection supports `metrics_cardinality` to control this:

- `detailed` (default): `key` / `upstream` label values are the real values (debug-friendly, potentially high cardinality).
- `coarse`: collapses the `key` / `upstream` label values to the constant `__all__` to bound time series; miss `reason` remains labeled.

### Miss reasons (route_misses_total.reason)

In addition to the existing reasons, the core path may emit:

- `disabled`: config exists but `enabled=false`, routes fall back.
- `expired`: config exists but is expired by `ttl` (requires `updated_at`), routes fall back.

---

## Event System

This repository provides an **in-process, synchronous** event bus (`EventBus`) and an optional event emitter (`EventEmitter`).

- **Subscribe**: register callbacks via `GetEventBus().Subscribe(eventName, handler)`; subscribing to `"*"` receives all events.
- **Publish**: `Publish` invokes handlers synchronously in registration order; no persistence and no cross-process delivery.
- **Important**: whether events actually occur depends on whether runtime code calls `EventEmitter.Emit*`. The core routing path and data source implementations **do not emit these events by default** today (they exist primarily for future wiring/external integration).

| Event | Data Type | Trigger |
|-------|-----------|---------|
| `dynamic_lb.route_selected` | RouteSelectedEvent | When `EmitRouteSelected` is called |
| `dynamic_lb.route_missed` | RouteMissedEvent | When `EmitRouteMissed` is called |
| `dynamic_lb.config_updated` | ConfigUpdatedEvent | When `EmitConfigUpdated` is called |
| `dynamic_lb.config_deleted` | ConfigDeletedEvent | When `EmitConfigDeleted` is called |
| `dynamic_lb.datasource_health_changed` | DataSourceHealthChangedEvent | When `EmitDataSourceHealthChanged` is called |

---

## Known Limitations

1. **least_conn algorithm**: Uses local counter, not shared across instances
2. **Admin API stats semantics**:
    - `/dynamic-lb/routes` returns a per-data-source-instance local cache snapshot (only keys that have been queried/preloaded and are present in cache).
    - `/dynamic-lb/policies` returns the in-process registry of `lb_policy dynamic` instances (debug/ops visibility only; does not affect routing); it includes a summary like key expression, data source type/module ID, and fallback policy module ID.
    - `/dynamic-lb/cache` returns both aggregated totals and per-instance stats under `sources`; `DELETE /dynamic-lb/cache` clears caches for all registered data source instances.
    - `/dynamic-lb/stats` `route_hits/route_misses` are in-process counters (reset on process restart/hotreload); Prometheus metrics remain the more authoritative source for long-term aggregation.
3. **Caddyfile supports only a subset of data sources**: currently etcd / redis / consul / file / http; other data sources require JSON config (including composite / sql / zookeeper / kubernetes).
    - For `lb_policy dynamic { ... }`: parsed by `DynamicSelection.UnmarshalCaddyfile` and delegated to each data source module's `UnmarshalCaddyfile` (so richer nested blocks/params may be supported depending on the data source).
    - For `dynamic_upstreams { ... }`: parsed by `DynamicUpstreams.UnmarshalCaddyfile` and is currently a **simplified key/value parser** (multi-value only for `endpoints`/`addresses`; no nested blocks like `tls { ... }`).

---

## Quick Reference

### Docker Test Environment

See [examples/docker/README.md](examples/docker/README.md).

### Test Routing

See [examples/docker/README.md](examples/docker/README.md).

### Set Routes (etcd)

See:

- [examples/basic/README.md](examples/basic/README.md) (basic etcd routing + JSON format)
- [examples/multi-tenant/README.md](examples/multi-tenant/README.md) (includes rules/conditional routing examples)

### View Routes

See [examples/docker/README.md](examples/docker/README.md) (includes etcdctl view/manage commands).
