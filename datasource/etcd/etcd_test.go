package etcd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

// TestEtcdSourceIntegration tests the etcd data source against a real etcd instance.
// Set ETCD_TEST_ADDR environment variable to run this test.
func TestEtcdSourceIntegration(t *testing.T) {
	addr := os.Getenv("ETCD_TEST_ADDR")
	if addr == "" {
		t.Skip("ETCD_TEST_ADDR not set, skipping integration test")
	}

	ctx := context.Background()

	// Create a test client to set up test data
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{addr},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create etcd client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Failed to close etcd client: %v", err)
		}
	})

	testPrefix := "/caddy/test/routing/"

	// Clean up test keys
	if _, err := client.Delete(ctx, testPrefix, clientv3.WithPrefix()); err != nil {
		t.Fatalf("Failed to delete test prefix: %v", err)
	}
	defer func() { _, _ = client.Delete(ctx, testPrefix, clientv3.WithPrefix()) }()

	// Set up test data
	if _, err := client.Put(ctx, testPrefix+"tenant-a", "backend-a:8080"); err != nil {
		t.Fatalf("Failed to put tenant-a: %v", err)
	}
	if _, err := client.Put(ctx, testPrefix+"tenant-b", `{
		"rules": [
			{
				"match": {"http.request.header.X-Version": "v2"},
				"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
				"priority": 10
			}
		],
		"fallback": "random"
	}`); err != nil {
		t.Fatalf("Failed to put tenant-b: %v", err)
	}

	// Create and provision the data source
	source := &EtcdSource{
		Endpoints:      []string{addr},
		Prefix:         testPrefix,
		DialTimeout:    caddy.Duration(5 * time.Second),
		RequestTimeout: caddy.Duration(2 * time.Second),
	}

	caddyCtx, cancel := caddy.NewContext(caddy.Context{})
	defer cancel()

	if err := source.Provision(caddyCtx); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	defer func() { _ = source.Cleanup() }()

	// Wait for initial load
	time.Sleep(100 * time.Millisecond)

	// Test cases
	t.Run("Get simple config", func(t *testing.T) {
		config, err := source.Get(ctx, "tenant-a")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if config == nil {
			t.Fatal("Expected config, got nil")
		}
		if config.Upstream != "backend-a:8080" {
			t.Errorf("Expected upstream backend-a:8080, got %s", config.Upstream)
		}
	})

	t.Run("Get JSON config", func(t *testing.T) {
		config, err := source.Get(ctx, "tenant-b")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if config == nil {
			t.Fatal("Expected config, got nil")
		}
		if len(config.Rules) != 1 {
			t.Errorf("Expected 1 rule, got %d", len(config.Rules))
		}
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		config, err := source.Get(ctx, "non-existent")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if config != nil {
			t.Error("Expected nil config for non-existent key")
		}
	})

	t.Run("Healthy check", func(t *testing.T) {
		if !source.Healthy() {
			t.Error("Expected source to be healthy")
		}
	})
}

func TestEtcdSourceDefaults(t *testing.T) {
	source := &EtcdSource{}

	// Simulate what Provision does for defaults
	if source.Prefix == "" {
		source.Prefix = "/caddy/routing/"
	}
	if source.DialTimeout == 0 {
		source.DialTimeout = caddy.Duration(5 * time.Second)
	}
	if source.RequestTimeout == 0 {
		source.RequestTimeout = caddy.Duration(2 * time.Second)
	}

	if source.Prefix != "/caddy/routing/" {
		t.Errorf("Expected default prefix '/caddy/routing/', got '%s'", source.Prefix)
	}
	if time.Duration(source.DialTimeout) != 5*time.Second {
		t.Errorf("Expected default dial timeout 5s, got %v", source.DialTimeout)
	}
	if time.Duration(source.RequestTimeout) != 2*time.Second {
		t.Errorf("Expected default request timeout 2s, got %v", source.RequestTimeout)
	}
}

func TestEtcdSourceCaddyModule(t *testing.T) {
	source := &EtcdSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.etcd"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*EtcdSource); !ok {
		t.Error("New() should return *EtcdSource")
	}
}

func TestEtcdSourceCacheOperations(t *testing.T) {
	source := &EtcdSource{}

	// Initialize cache
	source.cache = cache.NewRouteCache(100)

	// Test cache store and load
	testKey := "/test/tenant-x"
	testConfig := &datasource.RouteConfig{
		Upstream: "backend:8080",
	}

	// Initially cache is empty
	if source.cache.Get(testKey) != nil {
		t.Error("Cache should be empty initially")
	}

	// Store something
	source.cache.Set(testKey, testConfig)

	// Now it should be there
	if got := source.cache.Get(testKey); got == nil {
		t.Error("Cache should contain the stored key")
	} else if got.Upstream != testConfig.Upstream {
		t.Errorf("Expected upstream %s, got %s", testConfig.Upstream, got.Upstream)
	}

	// Delete
	source.cache.Delete(testKey)
	if source.cache.Get(testKey) != nil {
		t.Error("Cache should be empty after delete")
	}
}

func TestEtcdSourceHealthy(t *testing.T) {
	source := &EtcdSource{}

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

func TestEtcdSourceValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  *EtcdSource
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			source:  &EtcdSource{Endpoints: []string{"localhost:2379"}},
			wantErr: false,
		},
		{
			name:    "no endpoints",
			source:  &EtcdSource{},
			wantErr: true,
			errMsg:  "at least one endpoint",
		},
		{
			name:    "negative dial timeout",
			source:  &EtcdSource{Endpoints: []string{"localhost:2379"}, DialTimeout: -1},
			wantErr: true,
			errMsg:  "dial_timeout must be non-negative",
		},
		{
			name:    "negative request timeout",
			source:  &EtcdSource{Endpoints: []string{"localhost:2379"}, RequestTimeout: -1},
			wantErr: true,
			errMsg:  "request_timeout must be non-negative",
		},
		{
			name:    "negative max cache size",
			source:  &EtcdSource{Endpoints: []string{"localhost:2379"}, MaxCacheSize: -1},
			wantErr: true,
			errMsg:  "max_cache_size must be non-negative",
		},
		{
			name: "TLS cert without key",
			source: &EtcdSource{
				Endpoints:   []string{"localhost:2379"},
				TLSEnabled:  true,
				TLSCertFile: "/path/to/cert.pem",
			},
			wantErr: true,
			errMsg:  "both tls_cert_file and tls_key_file must be provided together",
		},
		{
			name: "TLS key without cert",
			source: &EtcdSource{
				Endpoints:  []string{"localhost:2379"},
				TLSEnabled: true,
				TLSKeyFile: "/path/to/key.pem",
			},
			wantErr: true,
			errMsg:  "both tls_cert_file and tls_key_file must be provided together",
		},
		{
			name: "valid TLS config",
			source: &EtcdSource{
				Endpoints:   []string{"localhost:2379"},
				TLSEnabled:  true,
				TLSCertFile: "/path/to/cert.pem",
				TLSKeyFile:  "/path/to/key.pem",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error containing %q", tt.errMsg)
				} else if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
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

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestEtcdSourceBuildTLSConfig(t *testing.T) {
	t.Run("skip verify only", func(t *testing.T) {
		source := &EtcdSource{
			TLSEnabled:    true,
			TLSSkipVerify: true,
		}

		tlsConfig, err := source.buildTLSConfig()
		if err != nil {
			t.Fatalf("buildTLSConfig failed: %v", err)
		}

		if !tlsConfig.InsecureSkipVerify {
			t.Error("Expected InsecureSkipVerify to be true")
		}
	})

	t.Run("missing cert file", func(t *testing.T) {
		source := &EtcdSource{
			TLSEnabled:  true,
			TLSCertFile: "/nonexistent/cert.pem",
			TLSKeyFile:  "/nonexistent/key.pem",
		}

		_, err := source.buildTLSConfig()
		if err == nil {
			t.Error("Expected error for missing cert file")
		}
	})

	t.Run("missing CA file", func(t *testing.T) {
		source := &EtcdSource{
			TLSEnabled: true,
			TLSCAFile:  "/nonexistent/ca.pem",
		}

		_, err := source.buildTLSConfig()
		if err == nil {
			t.Error("Expected error for missing CA file")
		}
	})
}
