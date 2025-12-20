#!/bin/bash
# etcd-setup.sh - Example script for setting up routing rules in etcd

# Simple routing: tenant-abc -> backend-abc:8080
etcdctl put /caddy/routing/tenant-abc "backend-abc:8080"

# Simple routing: tenant-xyz -> backend-xyz:8080
etcdctl put /caddy/routing/tenant-xyz "backend-xyz:8080"

# Advanced routing with JSON rules
# This example routes based on X-Version header with weighted upstreams
etcdctl put /caddy/routing/tenant-advanced '{
  "rules": [
    {
      "match": {"http.request.header.X-Version": "v2", "http.request.header.X-Canary": "true"},
      "upstreams": [{"address": "backend-v2-canary:8080", "weight": 100}],
      "priority": 10
    },
    {
      "match": {"http.request.header.X-Version": "v2"},
      "upstreams": [
        {"address": "backend-v2-a:8080", "weight": 80},
        {"address": "backend-v2-b:8080", "weight": 20}
      ],
      "priority": 5
    },
    {
      "match": {},
      "upstreams": [{"address": "backend-v1:8080", "weight": 100}],
      "priority": 0
    }
  ],
  "fallback": "round_robin",
  "metadata": {
    "tenant": "advanced",
    "environment": "production"
  }
}'

# Routing with regex matching
etcdctl put /caddy/routing/tenant-regex '{
  "rules": [
    {
      "match": {"http.request.uri.path": "~^/api/v[0-9]+/"},
      "upstreams": [{"address": "api-backend:8080"}],
      "priority": 10
    },
    {
      "match": {"http.request.uri.path": "~^/static/"},
      "upstreams": [{"address": "static-backend:8080"}],
      "priority": 5
    }
  ],
  "fallback": "random"
}'

echo "Routing rules configured successfully!"
echo ""
echo "View all routing rules:"
etcdctl get /caddy/routing/ --prefix

echo ""
echo "Watch for changes:"
echo "  etcdctl watch /caddy/routing/ --prefix"
