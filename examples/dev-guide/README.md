# Development Guide

This document contains command snippets and operational checks for developing and debugging **caddy-dynamic-routing**.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Quick start: [../basic/README.md](../basic/README.md)
- Docker environment: [../docker/README.md](../docker/README.md)
- Admin API: [../admin-api/README.md](../admin-api/README.md)
- Metrics: [../metrics/README.md](../metrics/README.md)

## Setup

```bash
go mod download
go test ./...
```

## Build / install (xcaddy)

This repository is a Caddy module; it is typically built into a Caddy binary via `xcaddy`.

```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing
```

## Tooling

```bash
# Install xcaddy (Caddy builder with plugins)
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Install golangci-lint v2
# (Matches this repository's expected major version)
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2

xcaddy version
$(go env GOPATH)/bin/golangci-lint version
```

## Common checks

```bash
# Tests
go test ./...

# Lint
make lint

# Format
make fmt
```

Note: in some environments `golangci-lint` may not be on `PATH`. This repo’s `make lint` expects it to be available; otherwise you can run:

```bash
$(go env GOPATH)/bin/golangci-lint run ./...
```

## Prometheus metrics quick check

```bash
# If your Caddy setup exposes metrics on the admin listener:
# (Example endpoint; adjust to your environment)
curl -s http://localhost:2019/metrics | grep -E '^caddy_dynamic_lb_' || true
```

## Admin API quick check

See [../admin-api/README.md](../admin-api/README.md).

## Docker test environment

See [../docker/README.md](../docker/README.md).

## More dev docs

- [code-style.md](code-style.md)
- [adding-datasource.md](adding-datasource.md)
- [adding-algorithm.md](adding-algorithm.md)
- [git-workflow.md](git-workflow.md)
- [testing.md](testing.md)
