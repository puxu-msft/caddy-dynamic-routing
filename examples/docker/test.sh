#!/bin/bash
# test.sh - smoke test for caddy-dynamic-routing docker environment

set -euo pipefail

CADDY_URL="http://localhost:8080"

echo "============================================"
echo "  caddy-dynamic-routing Docker Smoke Test"
echo "============================================"
echo ""

echo "Waiting briefly for services to be ready..."
sleep 2

echo ""
echo "=== Test 1: Route to tenant-a ==="
curl -s -H "X-Tenant: tenant-a" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 2: Route to tenant-b ==="
curl -s -H "X-Tenant: tenant-b" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 3: No tenant header (fallback) ==="
curl -s "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 4: Unknown tenant (fallback) ==="
curl -s -H "X-Tenant: unknown" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 5: Conditional routing (tenant-advanced, X-Version: v2) ==="
curl -s -H "X-Tenant: tenant-advanced" -H "X-Version: v2" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 6: Conditional routing (tenant-advanced, no X-Version) ==="
curl -s -H "X-Tenant: tenant-advanced" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 7: Dynamic update - add new tenant ==="
docker exec caddy-dynamic-routing-etcd etcdctl put /caddy/routing/tenant-c "backend-default:5678"
sleep 1
curl -s -H "X-Tenant: tenant-c" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 8: Dynamic update - modify existing tenant ==="
docker exec caddy-dynamic-routing-etcd etcdctl put /caddy/routing/tenant-a "backend-b:5678"
sleep 1
curl -s -H "X-Tenant: tenant-a" "$CADDY_URL" || true
echo ""
echo ""

echo "=== Test 9: View current routing rules in etcd ==="
docker exec caddy-dynamic-routing-etcd etcdctl get /caddy/routing/ --prefix

echo ""
echo "============================================"
echo "  Done"
echo "============================================"
