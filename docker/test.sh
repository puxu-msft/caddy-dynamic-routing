#!/bin/bash
# test.sh - 测试 caddy-dynamic-routing 动态路由功能

set -e

CADDY_URL="http://localhost:8080"
ETCD_URL="http://localhost:2379"

echo "============================================"
echo "  caddy-dynamic-routing Dynamic Selection Policy Test"
echo "============================================"
echo ""

# 等待服务就绪
echo "Waiting for services to be ready..."
sleep 2

echo ""
echo "=== Test 1: Route to tenant-a ==="
echo "Request: curl -H 'X-Tenant: tenant-a' $CADDY_URL"
curl -s -H "X-Tenant: tenant-a" $CADDY_URL
echo ""
echo ""

echo "=== Test 2: Route to tenant-b ==="
echo "Request: curl -H 'X-Tenant: tenant-b' $CADDY_URL"
curl -s -H "X-Tenant: tenant-b" $CADDY_URL
echo ""
echo ""

echo "=== Test 3: No tenant header (fallback to random) ==="
echo "Request: curl $CADDY_URL"
curl -s $CADDY_URL
echo ""
echo ""

echo "=== Test 4: Unknown tenant (fallback to random) ==="
echo "Request: curl -H 'X-Tenant: unknown' $CADDY_URL"
curl -s -H "X-Tenant: unknown" $CADDY_URL
echo ""
echo ""

echo "=== Test 5: Conditional routing - tenant-advanced with X-Version: v2 ==="
echo "Request: curl -H 'X-Tenant: tenant-advanced' -H 'X-Version: v2' $CADDY_URL"
curl -s -H "X-Tenant: tenant-advanced" -H "X-Version: v2" $CADDY_URL
echo ""
echo ""

echo "=== Test 6: Conditional routing - tenant-advanced without X-Version ==="
echo "Request: curl -H 'X-Tenant: tenant-advanced' $CADDY_URL"
curl -s -H "X-Tenant: tenant-advanced" $CADDY_URL
echo ""
echo ""

echo "=== Test 7: Dynamic update - add new tenant ==="
echo "Adding tenant-c -> backend-default"
docker exec caddy-dynamic-routing-etcd etcdctl put /caddy/routing/tenant-c "backend-default:5678"
sleep 1
echo "Request: curl -H 'X-Tenant: tenant-c' $CADDY_URL"
curl -s -H "X-Tenant: tenant-c" $CADDY_URL
echo ""
echo ""

echo "=== Test 8: Dynamic update - modify existing tenant ==="
echo "Changing tenant-a -> backend-b"
docker exec caddy-dynamic-routing-etcd etcdctl put /caddy/routing/tenant-a "backend-b:5678"
sleep 1
echo "Request: curl -H 'X-Tenant: tenant-a' $CADDY_URL"
curl -s -H "X-Tenant: tenant-a" $CADDY_URL
echo ""
echo ""

echo "=== Test 9: View current routing rules in etcd ==="
docker exec caddy-dynamic-routing-etcd etcdctl get /caddy/routing/ --prefix
echo ""

echo "============================================"
echo "  All tests completed!"
echo "============================================"
