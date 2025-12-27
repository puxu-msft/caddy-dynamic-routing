#!/bin/bash
# test.sh - smoke test for caddy-dynamic-routing docker environment

set -euo pipefail

CADDY_URL="http://localhost:8080"

etcdctl() {
	docker exec caddy-dynamic-routing-etcd etcdctl --endpoints=http://etcd:2379 "$@"
}

wait_for_http() {
	local url="$1"
	local attempts=40
	local i

	for i in $(seq 1 "$attempts"); do
		if curl -fsS --connect-timeout 1 --max-time 2 "$url" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done

	echo "Timed out waiting for $url" >&2
	return 1
}

expect_contains() {
	local name="$1"
	local expected="$2"
	shift 2

	local body
	body=$(curl -fsS --connect-timeout 2 --max-time 5 "$@")
	echo "$body"
	if ! echo "$body" | grep -Fq "$expected"; then
		echo "FAILED: $name (expected to contain: $expected)" >&2
		return 1
	fi
}

echo "============================================"
echo "  caddy-dynamic-routing Docker Smoke Test"
echo "============================================"
echo ""

echo "Waiting for Caddy to be ready..."
wait_for_http "$CADDY_URL"

echo "Resetting routing rules in etcd for deterministic test..."
etcdctl del /caddy/routing/ --prefix >/dev/null

etcdctl put /caddy/routing/tenant-a "backend-a:5678" >/dev/null
etcdctl put /caddy/routing/tenant-b "backend-b:5678" >/dev/null
etcdctl put /caddy/routing/tenant-advanced '{
	"rules": [
		{
			"match": {"http.request.header.X-Version": "v2"},
			"upstreams": [{"address": "backend-b:5678", "weight": 100}],
			"priority": 10
		},
		{
			"match": {},
			"upstreams": [{"address": "backend-a:5678", "weight": 100}],
			"priority": 0
		}
	],
	"fallback": "random"
}' >/dev/null

echo ""
echo "=== Test 1: Route to tenant-a ==="
expect_contains "tenant-a" "Hello from Backend A" -H "X-Tenant: tenant-a" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 2: Route to tenant-b ==="
expect_contains "tenant-b" "Hello from Backend B" -H "X-Tenant: tenant-b" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 3: No tenant header (fallback) ==="
expect_contains "fallback(no tenant)" "Hello from" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 4: Unknown tenant (fallback) ==="
expect_contains "fallback(unknown)" "Hello from" -H "X-Tenant: unknown" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 5: Conditional routing (tenant-advanced, X-Version: v2) ==="
expect_contains "tenant-advanced(v2)" "Hello from Backend B" -H "X-Tenant: tenant-advanced" -H "X-Version: v2" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 6: Conditional routing (tenant-advanced, no X-Version) ==="
expect_contains "tenant-advanced(default)" "Hello from Backend A" -H "X-Tenant: tenant-advanced" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 7: Dynamic update - add new tenant ==="
etcdctl put /caddy/routing/tenant-c "backend-default:5678" >/dev/null
sleep 1
expect_contains "tenant-c" "Hello from Default Backend" -H "X-Tenant: tenant-c" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 8: Dynamic update - modify existing tenant ==="
etcdctl put /caddy/routing/tenant-a "backend-b:5678" >/dev/null
sleep 1
expect_contains "tenant-a(updated)" "Hello from Backend B" -H "X-Tenant: tenant-a" "$CADDY_URL"
echo ""
echo ""

echo "=== Test 9: View current routing rules in etcd ==="
etcdctl get /caddy/routing/ --prefix

echo ""
echo "============================================"
echo "  Done"
echo "============================================"
