# Contributing

## Development Setup

```bash
git clone https://github.com/puxu-msft/caddy-dynamic-routing.git
cd caddy-dynamic-routing
go mod download
```

## Build & Test

```bash
# Build with xcaddy
xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing=./

# Run tests
go test ./...

# Run tests with race detector
go test -race ./...

# Run linter
golangci-lint run ./...

# Docker test environment
cd docker && docker compose up -d
```

## Code Style

- Use `gofmt` for formatting
- Add doc comments to all exported functions/types
- Use table-driven tests
- Add concurrent tests for thread-safe code

## Commit Messages

```
<type>: <description>

Types: feat, fix, docs, test, refactor, perf, chore
```

Example:
```
feat: add Zookeeper data source
```

## Adding a New Data Source

1. Create `datasource/mynewsource/mynewsource.go`
2. Implement `DataSource` interface
3. Register module in `init()`
4. Add tests
5. Update README.md

```go
package mynewsource

import (
    "github.com/caddyserver/caddy/v2"
    "github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

func init() {
    caddy.RegisterModule(&MyNewSource{})
}

type MyNewSource struct {
    // Configuration fields
}

func (*MyNewSource) CaddyModule() caddy.ModuleInfo {
    return caddy.ModuleInfo{
        ID:  "http.reverse_proxy.selection_policies.dynamic.sources.mynewsource",
        New: func() caddy.Module { return new(MyNewSource) },
    }
}

// Implement: Provision, Get, Healthy, Cleanup
```

## Pull Requests

1. Fork the repository
2. Create a feature branch
3. Run `go test -race ./...`
4. Submit PR with clear description
