# Docker Test Environment

Complete test environment with Caddy, etcd, and backends.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Quick start: [../basic/README.md](../basic/README.md)
- Admin API: [../admin-api/README.md](../admin-api/README.md)
- Metrics: [../metrics/README.md](../metrics/README.md)

## Quick Start

```bash
cd examples/docker
docker compose up --build -d
chmod +x ./test.sh
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

# Dynamic routing admin endpoints
curl http://localhost:2019/dynamic-lb/health | jq .
curl http://localhost:2019/dynamic-lb/routes | jq .
curl http://localhost:2019/dynamic-lb/policies | jq .
curl http://localhost:2019/dynamic-lb/cache | jq .
curl http://localhost:2019/dynamic-lb/stats | jq .
```

## Cleanup

```bash
docker compose down -v
```

Tip: if you already have `make docker-up` / `make docker-down` available, they use this same compose file.
