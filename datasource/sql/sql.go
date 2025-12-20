// Package sql provides a SQL database data source for dynamic routing configuration.
// Supports MySQL and PostgreSQL.
package sql

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	// Database drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

func init() {
	caddy.RegisterModule(&SQLSource{})
}

// SQLSource implements datasource.DataSource using SQL databases.
type SQLSource struct {
	// Driver is the database driver: "mysql" or "postgres".
	Driver string `json:"driver,omitempty"`

	// DSN is the data source name (connection string).
	// MySQL: user:password@tcp(host:port)/dbname
	// PostgreSQL: postgres://user:password@host:port/dbname?sslmode=disable
	DSN string `json:"dsn,omitempty"`

	// Table is the table name for routing configurations.
	// Default is "routing_configs".
	Table string `json:"table,omitempty"`

	// KeyColumn is the column name for the routing key.
	// Default is "routing_key".
	KeyColumn string `json:"key_column,omitempty"`

	// ConfigColumn is the column name for the configuration JSON.
	// Default is "config".
	ConfigColumn string `json:"config_column,omitempty"`

	// PollInterval is the interval between polling for changes.
	// Default is 30s. Set to 0 to disable polling.
	PollInterval caddy.Duration `json:"poll_interval,omitempty"`

	// MaxOpenConns is the maximum number of open connections.
	// Default is 10.
	MaxOpenConns int `json:"max_open_conns,omitempty"`

	// MaxIdleConns is the maximum number of idle connections.
	// Default is 5.
	MaxIdleConns int `json:"max_idle_conns,omitempty"`

	// ConnMaxLifetime is the maximum connection lifetime.
	// Default is 1h.
	ConnMaxLifetime caddy.Duration `json:"conn_max_lifetime,omitempty"`

	// MaxCacheSize is the maximum number of entries in the local cache.
	// Default is 10000.
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// internal fields
	db        *sql.DB
	cache     *cache.RouteCache
	healthy   atomic.Bool
	logger    *zap.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	pollWg    sync.WaitGroup
	lastCheck time.Time
	checkMu   sync.Mutex
	sfGroup   singleflight.Group // Coalesce concurrent requests for same key
}

// CaddyModule returns the Caddy module information.
func (*SQLSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.sql",
		New: func() caddy.Module { return new(SQLSource) },
	}
}

// Provision sets up the SQL data source.
func (s *SQLSource) Provision(ctx caddy.Context) error {
	s.logger = ctx.Logger()

	// Set defaults
	if s.Table == "" {
		s.Table = "routing_configs"
	}
	if s.KeyColumn == "" {
		s.KeyColumn = "routing_key"
	}
	if s.ConfigColumn == "" {
		s.ConfigColumn = "config"
	}
	if s.PollInterval == 0 {
		s.PollInterval = caddy.Duration(30 * time.Second)
	}
	if s.MaxOpenConns <= 0 {
		s.MaxOpenConns = 10
	}
	if s.MaxIdleConns <= 0 {
		s.MaxIdleConns = 5
	}
	if s.ConnMaxLifetime == 0 {
		s.ConnMaxLifetime = caddy.Duration(time.Hour)
	}
	if s.MaxCacheSize <= 0 {
		s.MaxCacheSize = 10000
	}

	// Initialize cache
	s.cache = cache.NewRouteCache(s.MaxCacheSize)

	// Create context for polling
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Connect to database
	db, err := sql.Open(s.Driver, s.DSN)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(s.MaxOpenConns)
	db.SetMaxIdleConns(s.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(s.ConnMaxLifetime))

	// Test connection
	if err := db.PingContext(s.ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	s.db = db
	s.healthy.Store(true)

	// Initial load
	if err := s.initialLoad(); err != nil {
		s.logger.Warn("initial database load failed", zap.Error(err))
	}

	// Start polling if enabled
	if s.PollInterval > 0 {
		s.pollWg.Add(1)
		go s.pollLoop()
	}

	s.logger.Info("sql data source initialized",
		zap.String("driver", s.Driver),
		zap.String("table", s.Table))

	return nil
}

// Cleanup releases database resources.
func (s *SQLSource) Cleanup() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.pollWg.Wait()
	if s.db != nil {
		s.db.Close()
	}
	s.logger.Info("sql data source cleaned up")
	return nil
}

// Get retrieves routing configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (s *SQLSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	// Check cache first
	if config := s.cache.Get(key); config != nil {
		return config, nil
	}

	// Check negative cache
	if s.cache.IsNegativeCached(key) {
		return nil, nil
	}

	// If unhealthy, don't try to fetch from database
	if !s.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := s.sfGroup.Do(key, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if config := s.cache.Get(key); config != nil {
			return config, nil
		}

		// Fetch from database
		query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1",
			s.ConfigColumn, s.Table, s.KeyColumn)

		// Adjust placeholder for MySQL
		if s.Driver == "mysql" {
			query = fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?",
				s.ConfigColumn, s.Table, s.KeyColumn)
		}

		var configData string
		err := s.db.QueryRowContext(ctx, query, key).Scan(&configData)
		if err != nil {
			if err == sql.ErrNoRows {
				// Cache negative result to avoid repeated lookups for missing keys
				s.cache.SetNegative(key)
				return nil, nil
			}
			s.healthy.Store(false)
			return nil, fmt.Errorf("failed to query config: %w", err)
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			s.logger.Warn("failed to parse route config",
				zap.String("key", key),
				zap.Error(err))
			return nil, nil
		}

		// Cache the result
		s.cache.Set(key, config)
		return config, nil
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// Healthy returns the health status of the database connection.
func (s *SQLSource) Healthy() bool {
	return s.healthy.Load()
}

// initialLoad loads all existing configurations from the database.
func (s *SQLSource) initialLoad() error {
	query := fmt.Sprintf("SELECT %s, %s FROM %s",
		s.KeyColumn, s.ConfigColumn, s.Table)

	rows, err := s.db.QueryContext(s.ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query configs: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var key, configData string
		if err := rows.Scan(&key, &configData); err != nil {
			s.logger.Warn("failed to scan row", zap.Error(err))
			continue
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			s.logger.Warn("failed to parse config", zap.String("key", key), zap.Error(err))
			continue
		}

		s.cache.Set(key, config)
		count++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("row iteration error: %w", err)
	}

	s.logger.Info("loaded initial configurations", zap.Int("count", count))
	return nil
}

// pollLoop periodically refreshes configurations from the database.
func (s *SQLSource) pollLoop() {
	defer s.pollWg.Done()

	ticker := time.NewTicker(time.Duration(s.PollInterval))
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.refresh(); err != nil {
				s.logger.Warn("failed to refresh configs", zap.Error(err))
				s.healthy.Store(false)
			} else {
				s.healthy.Store(true)
			}
		}
	}
}

// refresh reloads all configurations from the database.
func (s *SQLSource) refresh() error {
	query := fmt.Sprintf("SELECT %s, %s FROM %s",
		s.KeyColumn, s.ConfigColumn, s.Table)

	rows, err := s.db.QueryContext(s.ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query configs: %w", err)
	}
	defer rows.Close()

	// Track which keys we've seen
	seenKeys := make(map[string]bool)

	for rows.Next() {
		var key, configData string
		if err := rows.Scan(&key, &configData); err != nil {
			s.logger.Warn("failed to scan row", zap.Error(err))
			continue
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			s.logger.Warn("failed to parse config", zap.String("key", key), zap.Error(err))
			continue
		}

		s.cache.Set(key, config)
		seenKeys[key] = true
	}

	// Remove keys that no longer exist
	for _, key := range s.cache.Keys() {
		if !seenKeys[key] {
			s.cache.Delete(key)
		}
	}

	return rows.Err()
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (s *SQLSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "driver":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Driver = d.Val()
			case "dsn":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.DSN = d.Val()
			case "table":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Table = d.Val()
			case "key_column":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.KeyColumn = d.Val()
			case "config_column":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.ConfigColumn = d.Val()
			case "poll_interval":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid poll_interval: %v", err)
				}
				s.PollInterval = caddy.Duration(dur)
			case "max_open_conns":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var n int
				if _, err := fmt.Sscanf(d.Val(), "%d", &n); err != nil {
					return d.Errf("invalid max_open_conns: %v", err)
				}
				s.MaxOpenConns = n
			case "max_idle_conns":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var n int
				if _, err := fmt.Sscanf(d.Val(), "%d", &n); err != nil {
					return d.Errf("invalid max_idle_conns: %v", err)
				}
				s.MaxIdleConns = n
			case "max_cache_size":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var size int
				if _, err := fmt.Sscanf(d.Val(), "%d", &size); err != nil {
					return d.Errf("invalid max_cache_size: %v", err)
				}
				s.MaxCacheSize = size
			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// Validate implements caddy.Validator.
func (s *SQLSource) Validate() error {
	if s.Driver == "" {
		return fmt.Errorf("driver is required")
	}
	if s.Driver != "mysql" && s.Driver != "postgres" {
		return fmt.Errorf("driver must be 'mysql' or 'postgres'")
	}
	if s.DSN == "" {
		return fmt.Errorf("dsn is required")
	}
	if s.PollInterval < 0 {
		return fmt.Errorf("poll_interval must be non-negative")
	}
	if s.MaxOpenConns < 0 {
		return fmt.Errorf("max_open_conns must be non-negative")
	}
	if s.MaxIdleConns < 0 {
		return fmt.Errorf("max_idle_conns must be non-negative")
	}
	if s.MaxCacheSize < 0 {
		return fmt.Errorf("max_cache_size must be non-negative")
	}
	return nil
}

// CreateTableSQL returns the SQL to create the routing table.
func (s *SQLSource) CreateTableSQL() string {
	if s.Driver == "mysql" {
		return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id INT AUTO_INCREMENT PRIMARY KEY,
    %s VARCHAR(255) NOT NULL UNIQUE,
    %s JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_%s (%s)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
`, s.Table, s.KeyColumn, s.ConfigColumn, s.KeyColumn, s.KeyColumn)
	}

	// PostgreSQL
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id SERIAL PRIMARY KEY,
    %s VARCHAR(255) NOT NULL UNIQUE,
    %s JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_%s_%s ON %s (%s);
`, s.Table, s.KeyColumn, s.ConfigColumn, s.Table, s.KeyColumn, s.Table, s.KeyColumn)
}

// Interface guards
var (
	_ caddy.Module          = (*SQLSource)(nil)
	_ caddy.Provisioner     = (*SQLSource)(nil)
	_ caddy.CleanerUpper    = (*SQLSource)(nil)
	_ caddy.Validator       = (*SQLSource)(nil)
	_ datasource.DataSource = (*SQLSource)(nil)
	_ caddyfile.Unmarshaler = (*SQLSource)(nil)
)
