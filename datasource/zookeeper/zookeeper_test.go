package zookeeper

import (
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestZookeeperSourceCaddyModule(t *testing.T) {
	source := &ZookeeperSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.zookeeper"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*ZookeeperSource); !ok {
		t.Error("New() should return *ZookeeperSource")
	}
}

func TestZookeeperSourceDefaults(t *testing.T) {
	source := &ZookeeperSource{}

	// Simulate what Provision does for defaults
	if source.BasePath == "" {
		source.BasePath = "/caddy/routing"
	}
	if source.SessionTimeout == 0 {
		source.SessionTimeout = caddy.Duration(10 * 1e9) // 10s in nanoseconds
	}
	if source.MaxCacheSize <= 0 {
		source.MaxCacheSize = 10000
	}

	if source.BasePath != "/caddy/routing" {
		t.Errorf("Expected default base_path '/caddy/routing', got '%s'", source.BasePath)
	}
	if source.MaxCacheSize != 10000 {
		t.Errorf("Expected default max_cache_size 10000, got %d", source.MaxCacheSize)
	}
}

func TestZookeeperSourceValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  *ZookeeperSource
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			source:  &ZookeeperSource{Servers: []string{"localhost:2181"}},
			wantErr: false,
		},
		{
			name:    "multiple servers",
			source:  &ZookeeperSource{Servers: []string{"zk1:2181", "zk2:2181", "zk3:2181"}},
			wantErr: false,
		},
		{
			name:    "no servers",
			source:  &ZookeeperSource{},
			wantErr: true,
			errMsg:  "at least one server",
		},
		{
			name:    "server without port",
			source:  &ZookeeperSource{Servers: []string{"localhost"}},
			wantErr: true,
			errMsg:  "must include port",
		},
		{
			name:    "negative session timeout",
			source:  &ZookeeperSource{Servers: []string{"localhost:2181"}, SessionTimeout: -1},
			wantErr: true,
			errMsg:  "session_timeout must be non-negative",
		},
		{
			name:    "negative max cache size",
			source:  &ZookeeperSource{Servers: []string{"localhost:2181"}, MaxCacheSize: -1},
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

func TestZookeeperSourceHealthy(t *testing.T) {
	source := &ZookeeperSource{}

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

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
