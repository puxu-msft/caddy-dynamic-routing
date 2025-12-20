// Package datasource provides the interface and types for routing data sources.
package datasource

import (
	"context"

	"github.com/caddyserver/caddy/v2"
)

// DataSource is the interface for routing configuration backends.
// Implementations should handle caching internally.
type DataSource interface {
	caddy.Module
	caddy.Provisioner
	caddy.CleanerUpper

	// Get retrieves the route configuration for the given key.
	// Returns nil if no configuration is found for the key.
	// Implementations should use internal caching to minimize external lookups.
	Get(ctx context.Context, key string) (*RouteConfig, error)

	// Healthy returns true if the data source is connected and healthy.
	// When unhealthy, the data source should still serve cached data if available.
	Healthy() bool
}
