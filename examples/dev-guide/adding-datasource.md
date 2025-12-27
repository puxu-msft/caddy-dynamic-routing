# Adding a New Data Source

This document is the canonical place for the data-source skeleton and step-by-step example.

## Recommended approach

- Start from a similar existing implementation under `datasource/` (e.g. `etcd/`, `redis/`, `http/`).
- Implement `datasource.DataSource` and the required Caddy lifecycle interfaces.
- Ensure Admin API support via the optional `Admin*` methods when applicable.

## Skeleton (example)

```go
package mynewsource

import (
	"context"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

type MyNewSource struct {
	Address string `json:"address,omitempty"`

	healthy atomic.Bool
}

var (
	_ caddy.Module          = (*MyNewSource)(nil)
	_ caddy.Provisioner     = (*MyNewSource)(nil)
	_ caddy.CleanerUpper    = (*MyNewSource)(nil)
	_ caddy.Validator       = (*MyNewSource)(nil)
	_ datasource.DataSource = (*MyNewSource)(nil)
)

func init() {
	caddy.RegisterModule(&MyNewSource{})
}

func (MyNewSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.mynewsource",
		New: func() caddy.Module { return new(MyNewSource) },
	}
}

func (m *MyNewSource) Provision(ctx caddy.Context) error {
	m.healthy.Store(true)
	return nil
}

func (m *MyNewSource) Cleanup() error { return nil }

func (m *MyNewSource) Validate() error { return nil }

func (m *MyNewSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	return nil, nil
}

func (m *MyNewSource) Healthy() bool { return m.healthy.Load() }
```

## Tests

- Add unit tests for `Validate`, parsing, `Get`, and concurrency.
- Ensure `go test ./...` is clean.
