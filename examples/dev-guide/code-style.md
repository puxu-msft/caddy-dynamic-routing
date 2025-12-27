# Code Style and Conventions

This document contains code-style examples and patterns referenced from root docs.

## Formatting

- Use `gofmt -s`.
- Prefer tabs (Go standard).

## Naming conventions (examples)

```go
// Exported types: PascalCase
type RouteConfig struct { /* ... */ }

// Exported functions: PascalCase
func NewRouteCache(maxSize int) *RouteCache { /* ... */ return nil }

// Unexported: camelCase
func (r *RedisSource) refreshKey(ctx context.Context, key string) { /* ... */ }

// Constants: PascalCase for exported, camelCase for internal
const DefaultCacheSize = 10000
const defaultNegativeTTL = 30 * time.Second

// Acronyms: consistent casing
type HTTPSource struct { /* ... */ }        // HTTP, not Http
func (s *SQLSource) GetDSN() string { return "" } // DSN, not Dsn
```

## Documentation comments (example)

```go
// RouteConfig represents the routing configuration for a key.
// It supports both simple single-upstream configurations and
// complex rule-based routing with multiple upstreams.
type RouteConfig struct {
	Upstream string `json:"upstream,omitempty"`
}
```

## Error handling (example)

```go
if err != nil {
	return nil, fmt.Errorf("fetching key %s: %w", key, err)
}

## Interface guards (required)

Use compile-time checks to ensure your types implement the required interfaces.

```go
var (
	_ caddy.Module          = (*MySource)(nil)
	_ caddy.Provisioner     = (*MySource)(nil)
	_ caddy.CleanerUpper    = (*MySource)(nil)
	_ caddy.Validator       = (*MySource)(nil)
	_ datasource.DataSource = (*MySource)(nil)
	_ caddyfile.Unmarshaler = (*MySource)(nil)
)
```
```
