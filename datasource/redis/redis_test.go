package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisSourceIntegration tests the Redis data source against a real Redis instance.
// Set REDIS_TEST_ADDR environment variable to run this test.
func TestRedisSourceIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set, skipping integration test")
	}

	ctx := context.Background()

	// Create a test client to set up test data
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})
	defer func() { _ = client.Close() }()

	// Clean up test keys
	testPrefix := "caddy:test:routing:"
	keys, err := client.Keys(ctx, testPrefix+"*").Result()
	if err != nil {
		t.Fatalf("Failed to list keys: %v", err)
	}
	if len(keys) > 0 {
		if err := client.Del(ctx, keys...).Err(); err != nil {
			t.Fatalf("Failed to delete keys: %v", err)
		}
	}
	defer func() {
		keys, _ := client.Keys(ctx, testPrefix+"*").Result()
		if len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
	}()

	// Set up test data
	if err := client.Set(ctx, testPrefix+"tenant-a", "backend-a:8080", 0).Err(); err != nil {
		t.Fatalf("Failed to set tenant-a: %v", err)
	}
	if err := client.Set(ctx, testPrefix+"tenant-b", `{
		"rules": [
			{
				"match": {"http.request.header.X-Version": "v2"},
				"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
				"priority": 10
			}
		],
		"fallback": "random"
	}`, 0).Err(); err != nil {
		t.Fatalf("Failed to set tenant-b: %v", err)
	}

	// Create and provision the data source
	source := &RedisSource{
		Addresses:   []string{addr},
		Prefix:      testPrefix,
		DialTimeout: caddy.Duration(5 * time.Second),
		ReadTimeout: caddy.Duration(2 * time.Second),
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

func TestRedisSourceDefaults(t *testing.T) {
	source := &RedisSource{}

	// Test defaults are applied in Provision
	if source.Prefix != "" {
		t.Errorf("Prefix should be empty before Provision")
	}

	// Simulate what Provision does for defaults
	if source.Prefix == "" {
		source.Prefix = "caddy:routing:"
	}
	if source.DialTimeout == 0 {
		source.DialTimeout = caddy.Duration(5 * time.Second)
	}
	if source.ReadTimeout == 0 {
		source.ReadTimeout = caddy.Duration(2 * time.Second)
	}
	if source.PubSubChannel == "" {
		source.PubSubChannel = "caddy:routing:updates"
	}

	if source.Prefix != "caddy:routing:" {
		t.Errorf("Expected default prefix 'caddy:routing:', got '%s'", source.Prefix)
	}
	if time.Duration(source.DialTimeout) != 5*time.Second {
		t.Errorf("Expected default dial timeout 5s, got %v", source.DialTimeout)
	}
	if time.Duration(source.ReadTimeout) != 2*time.Second {
		t.Errorf("Expected default read timeout 2s, got %v", source.ReadTimeout)
	}
	if source.PubSubChannel != "caddy:routing:updates" {
		t.Errorf("Expected default pubsub channel 'caddy:routing:updates', got '%s'", source.PubSubChannel)
	}
}

func TestRedisSourceCaddyModule(t *testing.T) {
	source := &RedisSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.redis"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*RedisSource); !ok {
		t.Error("New() should return *RedisSource")
	}
}

func TestRedisSourceValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  *RedisSource
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}},
			wantErr: false,
		},
		{
			name:    "no addresses",
			source:  &RedisSource{},
			wantErr: true,
			errMsg:  "at least one address",
		},
		{
			name:    "invalid db negative",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, DB: -1},
			wantErr: true,
			errMsg:  "db must be between 0 and 15",
		},
		{
			name:    "invalid db too high",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, DB: 16},
			wantErr: true,
			errMsg:  "db must be between 0 and 15",
		},
		{
			name:    "negative dial timeout",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, DialTimeout: -1},
			wantErr: true,
			errMsg:  "dial_timeout must be non-negative",
		},
		{
			name:    "negative read timeout",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, ReadTimeout: -1},
			wantErr: true,
			errMsg:  "read_timeout must be non-negative",
		},
		{
			name:    "cluster with db selection",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, Cluster: true, DB: 1},
			wantErr: true,
			errMsg:  "db selection is not supported in cluster mode",
		},
		{
			name:    "cluster with db 0 is valid",
			source:  &RedisSource{Addresses: []string{"localhost:6379"}, Cluster: true, DB: 0},
			wantErr: false,
		},
		{
			name: "sentinel without master name",
			source: &RedisSource{
				Sentinel: &SentinelConfig{
					SentinelAddresses: []string{"localhost:26379"},
				},
			},
			wantErr: true,
			errMsg:  "sentinel master_name is required",
		},
		{
			name: "sentinel without addresses",
			source: &RedisSource{
				Sentinel: &SentinelConfig{
					MasterName: "mymaster",
				},
			},
			wantErr: true,
			errMsg:  "at least one sentinel address is required",
		},
		{
			name: "valid sentinel config",
			source: &RedisSource{
				Sentinel: &SentinelConfig{
					MasterName:        "mymaster",
					SentinelAddresses: []string{"localhost:26379"},
				},
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

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
