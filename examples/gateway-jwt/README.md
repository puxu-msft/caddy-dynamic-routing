# gateway_jwt + instance/version routing example

This example demonstrates end-to-end “gateway decides exact backend” routing:

- `gateway_jwt` verifies an incoming JWT (via JWKS)
- the handler writes routing variables:
  - `http.vars.route_instance_key`
  - `http.vars.route_version_key`
- `lb_policy_dynamic` prefers the instance key, but falls back to the version key if the instance has **no available upstream**

Availability is determined by Caddy upstream availability (e.g. health checks, max_fails/circuit breaker, max requests).

Related: for the repo’s canonical Docker-based environment, see [../docker/](../docker/).

## What you run

You will run 3 things locally:

1) a local JWKS server that also prints a matching JWT
2) a few tiny backend servers
3) a Caddy build that includes this plugin

## Start the JWKS server (prints a JWT)

From the repo root:

```bash
go run ./examples/gateway-jwt/jwks-server --addr 127.0.0.1:9000
```

Keep it running. It prints an `Authorization: Bearer ...` token you’ll use with `curl`.

## Start backends

From the repo root, in separate terminals:

```bash
go run ./examples/gateway-jwt/backend --addr 127.0.0.1:9101 --name instance-i1
```

```bash
go run ./examples/gateway-jwt/backend --addr 127.0.0.1:9102 --name version-blue
```

```bash
go run ./examples/gateway-jwt/backend --addr 127.0.0.1:9103 --name fallback
```

Each backend serves:
- `/` (returns the backend name)
- `/health` (always 200 OK)

## Build Caddy with the plugin

This repo is a Caddy plugin; you need a custom build.

```bash
xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing=.
```

This produces `./caddy`.

## Run Caddy with the example Caddyfile

From the repo root:

```bash
./caddy run --config ./examples/gateway-jwt/Caddyfile --adapter caddyfile
```

Caddy listens on `:8080`.

## Test routing

Use the JWT printed by the JWKS server.

- When the instance backend is healthy, it should be preferred:

```bash
curl -H "Authorization: Bearer $JWT" http://127.0.0.1:8080/
```

Expected: response contains `instance-i1`.

- Stop the instance backend (the `:9101` process). Within ~1–2 seconds, the health check should mark it unavailable.

Run the same curl again:

```bash
curl -H "Authorization: Bearer $JWT" http://127.0.0.1:8080/
```

Expected: response contains `version-blue` (instance → version fallback).

## How the keys map

The JWKS server issues tokens with claims:

- `tenant = t1`
- `service = svc`
- `version = blue`
- `instance = i1`

And `gateway_jwt` builds keys using default templates:

- version key: `t1/svc/blue`
- instance key: `t1/svc/blue/i1`

These are looked up in `./examples/gateway-jwt/routes.json` using the file datasource.
