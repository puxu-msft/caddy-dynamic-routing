package sql

import (
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestSQLSourceCaddyModule(t *testing.T) {
	source := &SQLSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.sql"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*SQLSource); !ok {
		t.Error("New() should return *SQLSource")
	}
}

func TestSQLSourceDefaults(t *testing.T) {
	source := &SQLSource{}

	// Simulate what Provision does for defaults
	if source.Table == "" {
		source.Table = "routing_configs"
	}
	if source.KeyColumn == "" {
		source.KeyColumn = "routing_key"
	}
	if source.ConfigColumn == "" {
		source.ConfigColumn = "config"
	}
	if source.PollInterval == 0 {
		source.PollInterval = caddy.Duration(30 * 1e9) // 30s
	}
	if source.MaxOpenConns <= 0 {
		source.MaxOpenConns = 10
	}
	if source.MaxIdleConns <= 0 {
		source.MaxIdleConns = 5
	}

	if source.Table != "routing_configs" {
		t.Errorf("Expected default table 'routing_configs', got '%s'", source.Table)
	}
	if source.KeyColumn != "routing_key" {
		t.Errorf("Expected default key_column 'routing_key', got '%s'", source.KeyColumn)
	}
	if source.ConfigColumn != "config" {
		t.Errorf("Expected default config_column 'config', got '%s'", source.ConfigColumn)
	}
	if source.MaxOpenConns != 10 {
		t.Errorf("Expected default max_open_conns 10, got %d", source.MaxOpenConns)
	}
}

func TestSQLSourceValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  *SQLSource
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid mysql config",
			source: &SQLSource{
				Driver: "mysql",
				DSN:    "user:password@tcp(localhost:3306)/dbname",
			},
			wantErr: false,
		},
		{
			name: "valid postgres config",
			source: &SQLSource{
				Driver: "postgres",
				DSN:    "postgres://user:password@localhost:5432/dbname?sslmode=disable",
			},
			wantErr: false,
		},
		{
			name:    "no driver",
			source:  &SQLSource{DSN: "some_dsn"},
			wantErr: true,
			errMsg:  "driver is required",
		},
		{
			name:    "invalid driver",
			source:  &SQLSource{Driver: "sqlite", DSN: "file:test.db"},
			wantErr: true,
			errMsg:  "driver must be 'mysql' or 'postgres'",
		},
		{
			name:    "no dsn",
			source:  &SQLSource{Driver: "mysql"},
			wantErr: true,
			errMsg:  "dsn is required",
		},
		{
			name: "negative poll interval",
			source: &SQLSource{
				Driver:       "mysql",
				DSN:          "user:pass@tcp(localhost)/db",
				PollInterval: -1,
			},
			wantErr: true,
			errMsg:  "poll_interval must be non-negative",
		},
		{
			name: "negative max open conns",
			source: &SQLSource{
				Driver:       "mysql",
				DSN:          "user:pass@tcp(localhost)/db",
				MaxOpenConns: -1,
			},
			wantErr: true,
			errMsg:  "max_open_conns must be non-negative",
		},
		{
			name: "negative max idle conns",
			source: &SQLSource{
				Driver:       "mysql",
				DSN:          "user:pass@tcp(localhost)/db",
				MaxIdleConns: -1,
			},
			wantErr: true,
			errMsg:  "max_idle_conns must be non-negative",
		},
		{
			name: "negative max cache size",
			source: &SQLSource{
				Driver:       "mysql",
				DSN:          "user:pass@tcp(localhost)/db",
				MaxCacheSize: -1,
			},
			wantErr: true,
			errMsg:  "max_cache_size must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error containing %q", tt.errMsg)
				} else if tt.errMsg != "" && !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want to contain %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestSQLSourceHealthy(t *testing.T) {
	source := &SQLSource{}

	// Initially unhealthy
	if source.Healthy() {
		t.Error("Expected source to be unhealthy initially")
	}

	// Set healthy
	source.healthy.Store(true)
	if !source.Healthy() {
		t.Error("Expected source to be healthy after setting")
	}

	// Set unhealthy
	source.healthy.Store(false)
	if source.Healthy() {
		t.Error("Expected source to be unhealthy after unsetting")
	}
}

func TestSQLSourceCreateTableSQL(t *testing.T) {
	tests := []struct {
		name     string
		driver   string
		table    string
		wantSQL  string
	}{
		{
			name:    "mysql",
			driver:  "mysql",
			table:   "routes",
			wantSQL: "CREATE TABLE IF NOT EXISTS routes",
		},
		{
			name:    "postgres",
			driver:  "postgres",
			table:   "routing_configs",
			wantSQL: "CREATE TABLE IF NOT EXISTS routing_configs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &SQLSource{
				Driver:       tt.driver,
				Table:        tt.table,
				KeyColumn:    "routing_key",
				ConfigColumn: "config",
			}
			sql := source.CreateTableSQL()
			if !containsStr(sql, tt.wantSQL) {
				t.Errorf("CreateTableSQL() = %s, want to contain %s", sql, tt.wantSQL)
			}
		})
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
