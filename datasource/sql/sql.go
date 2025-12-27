// Package sql provides a SQL database data source for dynamic routing configuration.
// Supports MySQL and PostgreSQL.
package sql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
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
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
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

	// InitialLoadTimeout is the maximum time allowed for the initial load during
	// Provision.
	// Default: 30s
	InitialLoadTimeout caddy.Duration `json:"initial_load_timeout,omitempty"`

	// RefreshMode controls how the source refreshes its cache during polling.
	// Supported values: "full", "incremental".
	// Default: "full".
	RefreshMode string `json:"refresh_mode,omitempty"`

	// UpdatedAtColumn is the column used for incremental refresh cursor.
	// Only used when refresh_mode is "incremental".
	// Default: "updated_at".
	UpdatedAtColumn string `json:"updated_at_column,omitempty"`

	// DeletedAtColumn is an optional column indicating soft-deletion.
	// When set, rows where deleted_at_column IS NOT NULL are treated as deleted.
	// Applies to Get() and refresh.
	DeletedAtColumn string `json:"deleted_at_column,omitempty"`

	// FullRefreshInterval forces a periodic full refresh even in incremental
	// mode to reconcile hard deletes or cursor edge cases.
	// Set to 0 to disable.
	FullRefreshInterval caddy.Duration `json:"full_refresh_interval,omitempty"`

	// internal fields
	db        *sql.DB
	cache     *cache.RouteCache
	healthy   atomic.Bool
	logger    *zap.Logger
	adminName string
	lastError atomic.Value // string
	ctx       context.Context
	cancel    context.CancelFunc
	pollWg    sync.WaitGroup
	sfGroup   singleflight.Group // Coalesce concurrent requests for same key

	cursorMu        sync.Mutex
	incCursorTime   time.Time
	incCursorKey    string
	lastFullRefresh time.Time
}

const (
	refreshModeFull        = "full"
	refreshModeIncremental = "incremental"
)

var sqlIdentPattern = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*$`)

func validateSQLIdentifier(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if !sqlIdentPattern.MatchString(value) {
		return fmt.Errorf("%s contains invalid characters: %q", name, value)
	}
	for _, seg := range strings.Split(value, ".") {
		if seg == "" {
			return fmt.Errorf("%s contains empty identifier segment: %q", name, value)
		}
	}
	return nil
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
	if s.RefreshMode == "" {
		s.RefreshMode = refreshModeFull
	}
	if s.UpdatedAtColumn == "" {
		s.UpdatedAtColumn = "updated_at"
	}

	if err := validateSQLIdentifier("table", s.Table); err != nil {
		return err
	}
	if err := validateSQLIdentifier("key_column", s.KeyColumn); err != nil {
		return err
	}
	if err := validateSQLIdentifier("config_column", s.ConfigColumn); err != nil {
		return err
	}

	// Initialize cache
	s.cache = cache.NewRouteCache(s.MaxCacheSize)

	// Create context for polling
	s.ctx, s.cancel = context.WithCancel(ctx.Context)

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
		if closeErr := db.Close(); closeErr != nil {
			s.logger.Debug("failed to close db after ping failure", zap.Error(closeErr))
		}
		return fmt.Errorf("failed to ping database: %w", err)
	}

	s.db = db
	s.healthy.Store(true)

	// Initial load
	loadTimeout := time.Duration(s.InitialLoadTimeout)
	if loadTimeout == 0 {
		loadTimeout = datasource.DefaultInitialLoadTimeout
	}
	loadCtx, cancel := context.WithTimeout(s.ctx, loadTimeout)
	defer cancel()
	if err := s.initialLoad(loadCtx); err != nil {
		s.logger.Warn("initial database load failed", zap.Error(err))
		s.lastError.Store(err.Error())
	}

	// Initialize incremental cursor so we don't re-scan the whole table.
	if s.RefreshMode == refreshModeIncremental {
		if err := s.initIncrementalCursor(loadCtx); err != nil {
			s.logger.Warn("failed to initialize incremental cursor", zap.Error(err))
			s.lastError.Store(err.Error())
		}
	}

	// Start polling if enabled
	if s.PollInterval > 0 {
		s.pollWg.Add(1)
		go s.pollLoop()
	}

	s.logger.Info("sql data source initialized",
		zap.String("driver", s.Driver),
		zap.String("table", s.Table))

	// Register for Admin API inspection
	s.adminName = datasource.RegisterAdminSource(s)

	return nil
}

// Cleanup releases database resources.
func (s *SQLSource) Cleanup() error {
	datasource.UnregisterAdminSource(s.adminName)
	s.adminName = ""

	if s.cancel != nil {
		s.cancel()
	}
	s.pollWg.Wait()
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Warn("failed to close db", zap.Error(err))
		}
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
		// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
		query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1",
			s.ConfigColumn, s.Table, s.KeyColumn)
		if s.DeletedAtColumn != "" {
			// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
			query = fmt.Sprintf("%s AND %s IS NULL", query, s.DeletedAtColumn)
		}

		// Adjust placeholder for MySQL
		if s.Driver == "mysql" {
			// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
			query = fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?",
				s.ConfigColumn, s.Table, s.KeyColumn)
			if s.DeletedAtColumn != "" {
				// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
				query = fmt.Sprintf("%s AND %s IS NULL", query, s.DeletedAtColumn)
			}
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
			s.lastError.Store(err.Error())
			return nil, fmt.Errorf("failed to query config: %w", err)
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			metrics.RecordRouteConfigParseError(s.AdminType())
			s.logger.Warn("failed to parse route config",
				zap.String("key", key),
				zap.Error(err))
			s.lastError.Store(err.Error())
			return nil, nil
		}

		// Cache the result
		s.cache.Set(key, config)
		return config, nil
	})

	if err != nil {
		return nil, err
	}
	s.lastError.Store("")
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// AdminType returns the short type name for Admin API.
func (*SQLSource) AdminType() string { return "sql" }

// AdminLastError returns the most recent error message (best-effort).
func (s *SQLSource) AdminLastError() string {
	if v := s.lastError.Load(); v != nil {
		if str, ok := v.(string); ok {
			return str
		}
	}
	return ""
}

// AdminListRoutes returns a snapshot of cached routes.
func (s *SQLSource) AdminListRoutes() []datasource.AdminRouteInfo {
	if s.cache == nil {
		return nil
	}
	routes := make([]datasource.AdminRouteInfo, 0, s.cache.Len())
	s.cache.Range(func(k string, cfg *datasource.RouteConfig) bool {
		routes = append(routes, datasource.SummarizeRouteConfig(k, cfg))
		return true
	})
	return routes
}

// AdminCacheStats returns a snapshot of internal cache stats.
func (s *SQLSource) AdminCacheStats() datasource.AdminCacheStats {
	if s.cache == nil {
		return datasource.AdminCacheStats{}
	}
	h, m, nh, hr := s.cache.Stats()
	return datasource.AdminCacheStats{
		Entries:      s.cache.Len(),
		MaxSize:      s.cache.MaxSize(),
		Hits:         h,
		Misses:       m,
		NegativeHits: nh,
		HitRate:      hr,
	}
}

// AdminClearCache clears all internal caches.
func (s *SQLSource) AdminClearCache() {
	if s.cache != nil {
		s.cache.Clear()
	}
}

// Healthy returns the health status of the database connection.
func (s *SQLSource) Healthy() bool {
	return s.healthy.Load()
}

// initialLoad loads all existing configurations from the database.
func (s *SQLSource) initialLoad(ctx context.Context) error {
	// #nosec G201 -- identifiers are validated in Provision; values use placeholders where applicable.
	query := fmt.Sprintf("SELECT %s, %s FROM %s",
		s.KeyColumn, s.ConfigColumn, s.Table)
	if s.DeletedAtColumn != "" {
		// #nosec G201 -- identifiers are validated in Provision; values use placeholders where applicable.
		query = fmt.Sprintf("%s WHERE %s IS NULL", query, s.DeletedAtColumn)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query configs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.logger.Debug("failed to close rows", zap.Error(err))
		}
	}()

	count := 0
	for rows.Next() {
		var key, configData string
		if err := rows.Scan(&key, &configData); err != nil {
			s.logger.Warn("failed to scan row", zap.Error(err))
			continue
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			metrics.RecordRouteConfigParseError(s.AdminType())
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
			if err := s.refreshOnce(); err != nil {
				s.logger.Warn("failed to refresh configs", zap.Error(err))
				s.healthy.Store(false)
			} else {
				s.healthy.Store(true)
			}
		}
	}
}

func (s *SQLSource) refreshOnce() error {
	switch s.RefreshMode {
	case refreshModeFull:
		err := s.refreshFull()
		if err == nil {
			s.cursorMu.Lock()
			s.lastFullRefresh = time.Now()
			s.cursorMu.Unlock()
		}
		return err
	case refreshModeIncremental:
		// Optional periodic full reconcile.
		if time.Duration(s.FullRefreshInterval) > 0 {
			s.cursorMu.Lock()
			lastFull := s.lastFullRefresh
			s.cursorMu.Unlock()
			if lastFull.IsZero() || time.Since(lastFull) >= time.Duration(s.FullRefreshInterval) {
				if err := s.refreshFull(); err != nil {
					return err
				}
				s.cursorMu.Lock()
				s.lastFullRefresh = time.Now()
				s.cursorMu.Unlock()
				return nil
			}
		}
		return s.refreshIncremental()
	default:
		return fmt.Errorf("invalid refresh_mode: %q", s.RefreshMode)
	}
}

// refreshFull reloads all configurations from the database.
func (s *SQLSource) refreshFull() error {
	// #nosec G201 -- identifiers are validated in Provision; values use placeholders where applicable.
	query := fmt.Sprintf("SELECT %s, %s FROM %s",
		s.KeyColumn, s.ConfigColumn, s.Table)
	if s.DeletedAtColumn != "" {
		// #nosec G201 -- identifiers are validated in Provision; values use placeholders where applicable.
		query = fmt.Sprintf("%s WHERE %s IS NULL", query, s.DeletedAtColumn)
	}

	rows, err := s.db.QueryContext(s.ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query configs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.logger.Debug("failed to close rows", zap.Error(err))
		}
	}()

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
			metrics.RecordRouteConfigParseError(s.AdminType())
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

func (s *SQLSource) initIncrementalCursor(ctx context.Context) error {
	// #nosec G201 -- identifiers are validated in Provision.
	query := fmt.Sprintf("SELECT %s, %s FROM %s ORDER BY %s DESC, %s DESC LIMIT 1",
		s.UpdatedAtColumn, s.KeyColumn, s.Table, s.UpdatedAtColumn, s.KeyColumn)

	var ts time.Time
	var key string
	err := s.db.QueryRowContext(ctx, query).Scan(&ts, &key)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	s.cursorMu.Lock()
	s.incCursorTime = ts
	s.incCursorKey = key
	if s.lastFullRefresh.IsZero() {
		s.lastFullRefresh = time.Now()
	}
	s.cursorMu.Unlock()
	return nil
}

// refreshIncremental refreshes configurations changed since the last cursor.
func (s *SQLSource) refreshIncremental() error {
	s.cursorMu.Lock()
	cursorTime := s.incCursorTime
	cursorKey := s.incCursorKey
	s.cursorMu.Unlock()

	// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
	query := fmt.Sprintf(
		"SELECT %s, %s, %s FROM %s WHERE (%s > $1) OR (%s = $1 AND %s > $2) ORDER BY %s ASC, %s ASC",
		s.KeyColumn, s.ConfigColumn, s.UpdatedAtColumn, s.Table,
		s.UpdatedAtColumn, s.UpdatedAtColumn, s.KeyColumn,
		s.UpdatedAtColumn, s.KeyColumn,
	)
	args := []any{cursorTime, cursorKey}

	if s.DeletedAtColumn != "" {
		// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
		query = fmt.Sprintf(
			"SELECT %s, %s, %s, %s FROM %s WHERE (%s > $1) OR (%s = $1 AND %s > $2) ORDER BY %s ASC, %s ASC",
			s.KeyColumn, s.ConfigColumn, s.UpdatedAtColumn, s.DeletedAtColumn, s.Table,
			s.UpdatedAtColumn, s.UpdatedAtColumn, s.KeyColumn,
			s.UpdatedAtColumn, s.KeyColumn,
		)
	}

	if s.Driver == "mysql" {
		// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
		query = fmt.Sprintf(
			"SELECT %s, %s, %s FROM %s WHERE (%s > ?) OR (%s = ? AND %s > ?) ORDER BY %s ASC, %s ASC",
			s.KeyColumn, s.ConfigColumn, s.UpdatedAtColumn, s.Table,
			s.UpdatedAtColumn, s.UpdatedAtColumn, s.KeyColumn,
			s.UpdatedAtColumn, s.KeyColumn,
		)
		args = []any{cursorTime, cursorTime, cursorKey}
		if s.DeletedAtColumn != "" {
			// #nosec G201 -- identifiers are validated in Provision; values use placeholders.
			query = fmt.Sprintf(
				"SELECT %s, %s, %s, %s FROM %s WHERE (%s > ?) OR (%s = ? AND %s > ?) ORDER BY %s ASC, %s ASC",
				s.KeyColumn, s.ConfigColumn, s.UpdatedAtColumn, s.DeletedAtColumn, s.Table,
				s.UpdatedAtColumn, s.UpdatedAtColumn, s.KeyColumn,
				s.UpdatedAtColumn, s.KeyColumn,
			)
			args = []any{cursorTime, cursorTime, cursorKey}
		}
	}

	rows, err := s.db.QueryContext(s.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to query configs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.logger.Debug("failed to close rows", zap.Error(err))
		}
	}()

	maxTime := cursorTime
	maxKey := cursorKey

	for rows.Next() {
		var key, configData string
		var updatedAt time.Time
		var deletedAt sql.NullTime
		if s.DeletedAtColumn != "" {
			if err := rows.Scan(&key, &configData, &updatedAt, &deletedAt); err != nil {
				s.logger.Warn("failed to scan row", zap.Error(err))
				continue
			}
			if deletedAt.Valid {
				s.cache.Delete(key)
				if updatedAt.After(maxTime) || (updatedAt.Equal(maxTime) && key > maxKey) {
					maxTime = updatedAt
					maxKey = key
				}
				continue
			}
		} else {
			if err := rows.Scan(&key, &configData, &updatedAt); err != nil {
				s.logger.Warn("failed to scan row", zap.Error(err))
				continue
			}
		}

		config, err := datasource.ParseRouteConfig([]byte(configData))
		if err != nil {
			metrics.RecordRouteConfigParseError(s.AdminType())
			s.logger.Warn("failed to parse config", zap.String("key", key), zap.Error(err))
			continue
		}

		s.cache.Set(key, config)
		if updatedAt.After(maxTime) || (updatedAt.Equal(maxTime) && key > maxKey) {
			maxTime = updatedAt
			maxKey = key
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if maxTime.After(cursorTime) || (maxTime.Equal(cursorTime) && maxKey > cursorKey) {
		s.cursorMu.Lock()
		s.incCursorTime = maxTime
		s.incCursorKey = maxKey
		s.cursorMu.Unlock()
	}

	return nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (s *SQLSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		parseStringArg := func() (string, error) {
			if !d.NextArg() {
				return "", d.ArgErr()
			}
			val := d.Val()
			if args := d.RemainingArgs(); len(args) != 0 {
				return "", d.ArgErr()
			}
			return val, nil
		}
		parseDurationArg := func(name string) (caddy.Duration, error) {
			if !d.NextArg() {
				return 0, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return 0, d.Errf("invalid %s: %v", name, err)
			}
			if args := d.RemainingArgs(); len(args) != 0 {
				return 0, d.ArgErr()
			}
			return caddy.Duration(dur), nil
		}
		parseIntArg := func(name string) (int, error) {
			if !d.NextArg() {
				return 0, d.ArgErr()
			}
			var n int
			if _, err := fmt.Sscanf(d.Val(), "%d", &n); err != nil {
				return 0, d.Errf("invalid %s: %v", name, err)
			}
			if args := d.RemainingArgs(); len(args) != 0 {
				return 0, d.ArgErr()
			}
			return n, nil
		}

		handlers := map[string]func() error{
			"driver": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.Driver = v
				return nil
			},
			"dsn": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.DSN = v
				return nil
			},
			"table": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.Table = v
				return nil
			},
			"key_column": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.KeyColumn = v
				return nil
			},
			"config_column": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.ConfigColumn = v
				return nil
			},
			"poll_interval": func() error {
				dur, err := parseDurationArg("poll_interval")
				if err != nil {
					return err
				}
				s.PollInterval = dur
				return nil
			},
			"initial_load_timeout": func() error {
				dur, err := parseDurationArg("initial_load_timeout")
				if err != nil {
					return err
				}
				s.InitialLoadTimeout = dur
				return nil
			},
			"refresh_mode": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.RefreshMode = v
				return nil
			},
			"updated_at_column": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.UpdatedAtColumn = v
				return nil
			},
			"deleted_at_column": func() error {
				v, err := parseStringArg()
				if err != nil {
					return err
				}
				s.DeletedAtColumn = v
				return nil
			},
			"full_refresh_interval": func() error {
				dur, err := parseDurationArg("full_refresh_interval")
				if err != nil {
					return err
				}
				s.FullRefreshInterval = dur
				return nil
			},
			"max_open_conns": func() error {
				n, err := parseIntArg("max_open_conns")
				if err != nil {
					return err
				}
				s.MaxOpenConns = n
				return nil
			},
			"max_idle_conns": func() error {
				n, err := parseIntArg("max_idle_conns")
				if err != nil {
					return err
				}
				s.MaxIdleConns = n
				return nil
			},
			"max_cache_size": func() error {
				n, err := parseIntArg("max_cache_size")
				if err != nil {
					return err
				}
				s.MaxCacheSize = n
				return nil
			},
		}

		for nesting := d.Nesting(); d.NextBlock(nesting); {
			name := d.Val()
			h, ok := handlers[name]
			if !ok {
				return d.Errf("unrecognized subdirective: %s", name)
			}
			if err := h(); err != nil {
				return err
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
	if s.RefreshMode != "" && s.RefreshMode != refreshModeFull && s.RefreshMode != refreshModeIncremental {
		return fmt.Errorf("refresh_mode must be '%s' or '%s'", refreshModeFull, refreshModeIncremental)
	}
	if s.FullRefreshInterval < 0 {
		return fmt.Errorf("full_refresh_interval must be non-negative")
	}
	if s.RefreshMode == refreshModeIncremental {
		if s.UpdatedAtColumn == "" {
			return fmt.Errorf("updated_at_column is required when refresh_mode is '%s'", refreshModeIncremental)
		}
		if err := validateSQLIdentifier("updated_at_column", s.UpdatedAtColumn); err != nil {
			return err
		}
		if s.DeletedAtColumn != "" {
			if err := validateSQLIdentifier("deleted_at_column", s.DeletedAtColumn); err != nil {
				return err
			}
		}
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
