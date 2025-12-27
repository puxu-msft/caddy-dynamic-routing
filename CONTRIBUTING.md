# Contributing to caddy-dynamic-routing

This repository keeps root docs concise and moves runnable examples/code blocks into `examples/`.

If you are looking for commands (build/test/lint), start here: [examples/dev-guide/README.md](examples/dev-guide/README.md).

Quick links:

- [examples/README.md](examples/README.md)
- [examples/dev-guide/README.md](examples/dev-guide/README.md)
- [DESIGN.md](DESIGN.md)
- [README.md](README.md)

## Table of contents

- [Development environment](#development-environment)
- [IDE setup](#ide-setup)
- [Project structure](#project-structure)
- [Build / test / lint](#build--test--lint)
- [Code style](#code-style)
- [Testing](#testing)
- [Adding features](#adding-features)
- [Git / PR workflow](#git--pr-workflow)
- [Documentation rules](#documentation-rules)
- [Release](#release)

## Development environment

Prerequisites (high level):

- Go (see `go.mod` for the Go version this module targets)
- Git
- Docker + Docker Compose (optional; recommended for integration-style flows)
- `xcaddy` (to build a custom Caddy with this module)
- `golangci-lint` v2 (repo lint is pinned; see [examples/dev-guide/README.md](examples/dev-guide/README.md))

- Setup and common commands: [examples/dev-guide/README.md](examples/dev-guide/README.md)
- Build/install with `xcaddy`: [examples/dev-guide/README.md](examples/dev-guide/README.md)

## IDE setup

VS Code suggestions:

- Go (official Go extension)
- GitLens (optional)

GoLand suggestions:

- Enable gofmt on save
- Enable go vet on save

## Project structure

- Main routing module: `plugin.go`
- Caddyfile parsing: `caddyfile.go`
- Admin API endpoints (`/dynamic-lb/*`): `admin.go`
- Request key extraction: `extractor/`
- Matching + selection algorithms: `matcher/`
- Data sources: `datasource/`
- Metrics: `metrics/`
- Examples index: [examples/README.md](examples/README.md)
- Architecture notes: `DESIGN.md`

## Build / test / lint

See [examples/dev-guide/README.md](examples/dev-guide/README.md) and `Makefile` (`make check`).

## Code style

See [examples/dev-guide/code-style.md](examples/dev-guide/code-style.md).

## Testing

See [examples/dev-guide/testing.md](examples/dev-guide/testing.md).

## Adding features

- Add a data source: [examples/dev-guide/adding-datasource.md](examples/dev-guide/adding-datasource.md)
- Add a load-balancing algorithm: [examples/dev-guide/adding-algorithm.md](examples/dev-guide/adding-algorithm.md)

## Git / PR workflow

See [examples/dev-guide/git-workflow.md](examples/dev-guide/git-workflow.md).

## Documentation rules

- Keep `README.md` minimal; put complete runnable scenarios/configs under `examples/`.
- If code behavior changes, update `CLAUDE.md` first, then sync `CLAUDE_EN.md`.
- If you add/remove an Admin API endpoint or change response shape/semantics, update `admin_test.go` and the relevant docs (usually an example doc under `examples/`).

## Release

- Follow Semantic Versioning.
- Create an annotated tag (e.g. `v1.2.0`) and push it; CI/release automation will do the rest.
