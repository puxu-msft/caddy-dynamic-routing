# Claude 指令 - caddy-dynamic-routing

本文档包含项目的完整技术理解和开发规范，供 Claude 在协助开发时参考。

## 快速导航

- [文档定位](#文档定位重要)
- [项目概述](#项目概述)
- [构建和测试](#构建和测试)
- [核心接口](#核心接口)
- [性能优化](#性能优化)
- [编码规范](#编码规范)
- [测试规范](#测试规范)
- [用户偏好](#用户偏好)
- [模块 ID 列表](#模块-id-列表)

常用入口：

- examples 总览：[examples/README.md](examples/README.md)
- 开发指南（含安装/构建/检查命令）：[examples/dev-guide/README.md](examples/dev-guide/README.md)
- 指标说明：[examples/metrics/README.md](examples/metrics/README.md)
- Admin API 示例：[examples/admin-api/README.md](examples/admin-api/README.md)

## 文档定位（重要）

- **CLAUDE.md（中文）是权威源**：尽可能完整地记录对项目的理解、用户要求与协作偏好；出现冲突时以本文档为准。
- **CLAUDE_EN.md（英文）为同步译本**：用于英文读者/外部协作沟通；内容应与 CLAUDE.md 保持一致（允许因语言差异做轻微措辞调整，但不能遗漏约束与关键行为）。
- **README/CONTRIBUTING/DESIGN 的职责拆分**：README 面向用户快速入门；CONTRIBUTING 面向开发者“事无巨细”；DESIGN 面向架构与设计决策。本文档不替代它们，但会概括它们的约束并强调“哪些是必须遵守的”。

## 项目概述

**caddy-dynamic-routing** 是一个 Caddy 模块，为反向代理提供动态路由能力。它根据运行时配置（来自 etcd、Redis、Consul 等外部数据源）进行请求路由。

### 核心价值

与静态 Caddy 配置不同，本模块支持：
- **运行时路由变更**：无需重启 Caddy 即可更新路由配置
- **多租户路由**：SaaS 应用中按租户/客户分流
- **A/B 测试和金丝雀发布**：按条件分配流量到不同后端
- **多数据中心故障转移**：组合多个数据源实现高可用

### 项目结构

- `plugin.go`：DynamicSelection 主模块（实现 `reverseproxy.Selector`）
- `caddyfile.go`：Caddyfile 解析
- `admin.go`：Admin API（`/dynamic-lb/*`）
- `extractor/`：Key 提取
  - `extractor/extractor.go`：Placeholder 表达式解析（如 `{header.X-Tenant}`）
- `matcher/`：规则匹配与算法选择
  - `matcher/matcher.go`：条件匹配逻辑（支持正则等）
  - `matcher/selector.go`：负载均衡算法选择
- `datasource/`：数据源接口与实现
  - `datasource/types.go`：`RouteConfig`、`DataSource` 等核心类型与接口
  - `datasource/cache/`：LRU/负向缓存
  - `datasource/etcd/`、`datasource/redis/`、`datasource/consul/`、`datasource/file/`、`datasource/http/` 等：各数据源实现
- `metrics/`：Prometheus 指标与统计
- `tracing/`：Tracing helper
- `examples/`：所有可运行示例/命令/配置
- `docker/`：Docker 测试环境

---

## 构建和测试

命令与工具链请统一参考：

- [examples/dev-guide/README.md](examples/dev-guide/README.md)
（包含 `xcaddy` 构建/安装与常用检查命令）

---

## 核心接口

### DataSource 接口

所有数据源必须实现：

接口定义以代码为准：见 `datasource/types.go`。

### Caddy 模块接口

每个模块需要实现：

| 接口 | 方法 | 说明 |
|------|------|------|
| `caddy.Module` | `CaddyModule()` | 返回模块 ID 和构造函数 |
| `caddy.Provisioner` | `Provision(ctx)` | 初始化，获取 logger，建立连接 |
| `caddy.CleanerUpper` | `Cleanup()` | 释放资源，关闭连接 |
| `caddy.Validator` | `Validate()` | 验证配置有效性 |
| `caddyfile.Unmarshaler` | `UnmarshalCaddyfile()` | 解析 Caddyfile 配置 |

### Interface Guards 模式

**必须**使用编译时接口检查：

示例与模板见 [examples/dev-guide/code-style.md](examples/dev-guide/code-style.md)。

---

## 性能优化

已实现的性能优化：

| 优化项 | 位置 | 说明 |
|--------|------|------|
| **Singleflight** | 所有数据源 | 合并对同一 key 的并发请求，N→1 |
| **负向缓存** | cache/cache.go | 缓存"key not found"，避免重复查询 |
| **无锁 WRR** | matcher/selector.go | 使用 atomic 替代 mutex |
| **FNV Hash Pool** | matcher/selector.go | sync.Pool 复用 hash 对象 |
| **Upstream 索引** | plugin.go | O(1) 查找替代 O(n) 遍历 |
| **Builder Pool** | matcher/matcher.go | sync.Pool 复用 strings.Builder |

---

## 编码规范

### 添加新数据源

详见：`examples/dev-guide/adding-datasource.md`。

4. **注册模块**:
  参见：[examples/dev-guide/adding-datasource.md](examples/dev-guide/adding-datasource.md)。

5. **测试要求**:
   - `TestMyNewSource_Validate` - 配置验证
   - `TestMyNewSource_CaddyModule` - 模块注册
   - `TestMyNewSource_Get` - 基本功能
   - `TestMyNewSource_Concurrent` - 并发安全（如适用）

6. **更新文档**: README.md 和 DESIGN.md

### 错误处理规范

- 记录错误但不要让请求失败：`s.logger.Warn("...", zap.Error(err))`
- 连接失败时设置 `s.healthy.Store(false)`
- 不健康时返回 `nil, nil`，让 fallback 策略处理
- 所有外部调用使用 context timeout

### 日志规范

使用 `ctx.Logger()` 获取结构化 logger：

建议保持结构化字段（例如 `key`、`source_type`、错误信息），风格参考：`examples/dev-guide/code-style.md`。

日志级别：
- `Debug`: 详细调试信息
- `Info`: 正常操作事件（配置更新、启动完成）
- `Warn`: 可恢复的问题（连接失败、解析错误）
- `Error`: 严重问题（不应在正常运行中出现）

---

## 测试规范

### 测试类型

1. **单元测试**: 测试单个函数/方法
2. **表格驱动测试**: 多种输入场景
3. **并发测试**: 多线程安全性
4. **集成测试**: 需要外部服务（etcd, Redis 等）

### 测试模式

示例见：[examples/dev-guide/testing.md](examples/dev-guide/testing.md)。

### 并发测试模式

示例见：[examples/dev-guide/testing.md](examples/dev-guide/testing.md)。

### 运行测试

命令清单见：[examples/dev-guide/README.md](examples/dev-guide/README.md) 与 [examples/dev-guide/testing.md](examples/dev-guide/testing.md)。

---

## 用户偏好

### 提交规范

- **不要主动暂存或提交更改**，除非用户明确要求
- 提交时：
  - 使用描述性的提交信息
  - 遵循 `<type>: <description>` 格式
  - type: feat, fix, docs, test, refactor, perf, chore

### 代码更改

- 修改代码后**必须**运行测试
- 修改功能时更新相关测试
- 保持文档与代码一致
- 优先编辑现有文件，而非创建新文件

### 任务管理

- 多步骤任务使用 TODO 追踪
- 完成一个任务后再开始下一个
- 修改完成后用测试验证

### 文档职责

| 文档 | 职责 | 语言 |
|------|------|------|
| README.md | 简洁的项目介绍和快速入门 | 英文 |
| DESIGN.md | 架构设计和技术细节 | 英文 |
| CONTRIBUTING.md | 详细的开发指南 | 英文 |
| CLAUDE.md | 完整的项目理解和开发规范 | 中文 |
| CLAUDE_EN.md | CLAUDE.md 的英文版本 | 英文 |

---

## 模块 ID 列表

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

## Prometheus 指标

说明：指标在代码中使用 `namespace=caddy`、`subsystem=dynamic_lb` 组合生成，因此 Prometheus 侧最终名称为 `caddy_dynamic_lb_*`。

指标清单与标签基数说明参见：[examples/metrics/README.md](examples/metrics/README.md)。

注意：仓库里**定义**了完整的一组指标，但并非每个指标在当前版本都已被业务逻辑“写入/更新”。当前已在核心路径中稳定写入的主要包括：

- `route_hits_total` / `route_misses_total`（DynamicSelection）
- `datasource_latency_seconds`（DynamicSelection 与 DynamicUpstreams）
- `rule_match_latency_seconds`（DynamicSelection）
- `cache_hits_total` / `cache_misses_total`：当前主要来自 `DynamicUpstreams` 的 upstream 列表缓存（并不等同于各数据源内部 RouteCache 的命中率统计）

| 指标 | 类型 | 标签 | 说明 |
|------|------|------|------|
| `caddy_dynamic_lb_route_hits_total` | Counter | key, upstream | 路由命中次数 |
| `caddy_dynamic_lb_route_misses_total` | Counter | key, reason | 路由未命中次数 |
| `caddy_dynamic_lb_route_config_parse_errors_total` | Counter | source_type | 路由配置解析失败次数（按数据源类型统计，低基数） |
| `caddy_dynamic_lb_datasource_latency_seconds` | Histogram | source_type | 数据源查询延迟 |
| `caddy_dynamic_lb_cache_hits_total` | Counter | source_type | 缓存命中 |
| `caddy_dynamic_lb_cache_misses_total` | Counter | source_type | 缓存未命中 |
| `caddy_dynamic_lb_datasource_healthy` | Gauge | source_type | 数据源健康状态 |
| `caddy_dynamic_lb_active_upstreams` | Gauge | source_type | 当前活跃 upstream 数量（动态路由） |
| `caddy_dynamic_lb_route_configs_total` | Gauge | source_type | 当前缓存的路由配置数量 |
| `caddy_dynamic_lb_rule_match_latency_seconds` | Histogram | (none) | 规则匹配延迟 |
| `caddy_dynamic_lb_watch_events_total` | Counter | source_type, event_type | Watch 事件数量 |
| `caddy_dynamic_lb_pool_hits_total` | Counter | source_type, instance | 连接池命中 |
| `caddy_dynamic_lb_pool_misses_total` | Counter | source_type, instance | 连接池未命中 |
| `caddy_dynamic_lb_pool_timeouts_total` | Counter | source_type, instance | 连接池等待超时次数 |
| `caddy_dynamic_lb_pool_total_connections` | Gauge | source_type, instance | 总连接数 |
| `caddy_dynamic_lb_pool_idle_connections` | Gauge | source_type, instance | 空闲连接数 |
| `caddy_dynamic_lb_pool_stale_connections_total` | Counter | source_type, instance | 连接池清理的过期连接数 |

### 路由指标的标签基数（metrics_cardinality）

`route_hits_total` / `route_misses_total` 的 `key`（以及命中时的 `upstream`）在多租户/高 key 场景会导致 Prometheus 时序数量膨胀。

DynamicSelection 支持通过 `metrics_cardinality` 控制标签基数：

- `detailed`（默认）：`key` / `upstream` 使用真实值（调试友好，但可能产生高基数）。
- `coarse`：将 `key` / `upstream` 的标签值折叠为常量 `__all__`，用于限制时序数量；miss 的 `reason` 仍保留。

### Miss reason（route_misses_total.reason）补充

除已有原因外，核心路径还会使用：

- `disabled`：配置存在但 `enabled=false`，会直接 fallback。
- `expired`：配置存在但 `ttl` 已过期（依赖 `updated_at`），会直接 fallback。

---

## 事件系统

当前仓库提供一个**进程内、同步调用**的事件总线（`EventBus`）以及可选的事件发射器（`EventEmitter`）。

- **订阅**：通过 `GetEventBus().Subscribe(eventName, handler)` 注册回调；支持订阅 `"*"` 接收所有事件。
- **发布**：`Publish` 会按注册顺序同步调用 handler；无持久化、无跨进程传递。
- **重要**：事件是否真的发生取决于业务代码是否调用 `EventEmitter.Emit*`。目前核心路由路径/数据源实现并**不会自动**发射这些事件（主要用于未来接入或外部集成）。

| 事件 | 数据类型 | 触发时机 |
|------|----------|----------|
| `dynamic_lb.route_selected` | RouteSelectedEvent | 调用 `EmitRouteSelected` 时 |
| `dynamic_lb.route_missed` | RouteMissedEvent | 调用 `EmitRouteMissed` 时 |
| `dynamic_lb.config_updated` | ConfigUpdatedEvent | 调用 `EmitConfigUpdated` 时 |
| `dynamic_lb.config_deleted` | ConfigDeletedEvent | 调用 `EmitConfigDeleted` 时 |
| `dynamic_lb.datasource_health_changed` | DataSourceHealthChangedEvent | 调用 `EmitDataSourceHealthChanged` 时 |

---

## 已知限制

1. **least_conn 算法**：使用本地计数器，不跨实例共享
2. **Admin API 的统计口径**：
    - `/dynamic-lb/routes` 返回的是“各数据源实例”的本地缓存快照（只包含已被查询/预加载并进入缓存的 key）。
    - `/dynamic-lb/policies` 返回当前进程内已注册的 `lb_policy dynamic` 实例列表（用于排查/观测，不影响路由逻辑）；包含 key 表达式、数据源类型/模块 ID、fallback policy 模块 ID 等概要信息。
    - `/dynamic-lb/cache` 同时返回汇总统计与 `sources` 维度的逐实例统计；`DELETE /dynamic-lb/cache` 会对所有已注册的数据源实例执行清缓存。
    - `/dynamic-lb/stats` 的 `route_hits/route_misses` 是进程内计数（随进程重启/热加载重置）；Prometheus 指标仍是更权威/更适合长期聚合的统计来源。
3. **Caddyfile 支持的数据源有限**：当前仅支持 etcd / redis / consul / file / http；其他数据源需要使用 JSON 配置（包括 composite / sql / zookeeper / kubernetes 等）。
    - 对 `lb_policy dynamic { ... }`：由 `DynamicSelection.UnmarshalCaddyfile` 解析，并委托各数据源模块的 `UnmarshalCaddyfile`（因此支持更丰富的子块/参数，取决于数据源实现）。
    - 对 `dynamic_upstreams { ... }`：由 `DynamicUpstreams.UnmarshalCaddyfile` 解析，当前是**简化的 key/value 解析**（仅对 `endpoints`/`addresses` 支持多值；不支持诸如 `tls { ... }` 这类嵌套子块）。

---

## 快速参考

### Docker 测试环境

参见：[examples/docker/README.md](examples/docker/README.md)。

### 测试路由

参见：[examples/docker/README.md](examples/docker/README.md)。

### 设置路由（etcd）

参见：

- [examples/basic/README.md](examples/basic/README.md)（基础 etcd 路由与 JSON 格式）
- [examples/multi-tenant/README.md](examples/multi-tenant/README.md)（包含 rules/条件路由示例）

### 查看路由

参见：[examples/docker/README.md](examples/docker/README.md)（包含 etcdctl 的查看/管理命令）。
