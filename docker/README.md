# Docker Test Environment

Complete test environment with Caddy, etcd, and backends.

## Quick Start

```bash
docker compose up --build -d
./test.sh
```

## Services

| Service | Description |
|---------|-------------|
| caddy | Caddy server with dynamic routing |
| etcd | Configuration store |
| backend-a | Test backend for tenant-a |
| backend-b | Test backend for tenant-b |
| backend-default | Fallback backend |

## Test Routing

```bash
# Route to tenant-a
curl -H "X-Tenant: tenant-a" http://localhost:8080

# Route to tenant-b
curl -H "X-Tenant: tenant-b" http://localhost:8080

# Fallback (no tenant header)
curl http://localhost:8080

# Conditional routing
curl -H "X-Tenant: tenant-advanced" -H "X-Version: v2" http://localhost:8080
```

## Manage Routes

```bash
# View routes
docker exec caddy-dynamic-routing-etcd etcdctl get /caddy/routing/ --prefix

# Add route
docker exec caddy-dynamic-routing-etcd etcdctl put /caddy/routing/tenant-c "backend-default:5678"

# Delete route
docker exec caddy-dynamic-routing-etcd etcdctl del /caddy/routing/tenant-c
```

## View Logs

```bash
docker compose logs -f caddy
```

## Admin API

```bash
curl http://localhost:2019/config/ | jq .
```

## Cleanup

```bash
docker compose down -v
```
