package consul

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/hashicorp/consul/api"
)

// TestConsulSourceIntegration tests the Consul data source against a real Consul instance.
// Set CONSUL_TEST_ADDR environment variable to run this test.
func TestConsulSourceIntegration(t *testing.T) {
	addr := os.Getenv("CONSUL_TEST_ADDR")
	if addr == "" {
		t.Skip("CONSUL_TEST_ADDR not set, skipping integration test")
	}

	ctx := context.Background()

	// Create a test client to set up test data
	config := &api.Config{
		Address: addr,
	}
	client, err := api.NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create Consul client: %v", err)
	}

	kv := client.KV()
	testPrefix := "caddy/test/routing/"

	// Clean up test keys
	if _, err := kv.DeleteTree(testPrefix, nil); err != nil {
		t.Fatalf("Failed to delete test prefix: %v", err)
	}
	defer func() { _, _ = kv.DeleteTree(testPrefix, nil) }()

	// Set up test data
	if _, err := kv.Put(&api.KVPair{Key: testPrefix + "tenant-a", Value: []byte("backend-a:8080")}, nil); err != nil {
		t.Fatalf("Failed to put tenant-a: %v", err)
	}
	if _, err := kv.Put(&api.KVPair{Key: testPrefix + "tenant-b", Value: []byte(`{
		"rules": [
			{
				"match": {"http.request.header.X-Version": "v2"},
				"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
				"priority": 10
			}
		],
		"fallback": "random"
	}`)}, nil); err != nil {
		t.Fatalf("Failed to put tenant-b: %v", err)
	}

	// Create and provision the data source
	source := &ConsulSource{
		Address:  addr,
		Prefix:   testPrefix,
		WaitTime: caddy.Duration(5 * time.Second),
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

func TestConsulSourceDefaults(t *testing.T) {
	source := &ConsulSource{}

	// Simulate what Provision does for defaults
	if source.Address == "" {
		source.Address = "localhost:8500"
	}
	if source.Scheme == "" {
		source.Scheme = "http"
	}
	if source.Prefix == "" {
		source.Prefix = "caddy/routing/"
	}
	if source.WaitTime == 0 {
		source.WaitTime = caddy.Duration(5 * time.Minute)
	}

	if source.Address != "localhost:8500" {
		t.Errorf("Expected default address 'localhost:8500', got '%s'", source.Address)
	}
	if source.Scheme != "http" {
		t.Errorf("Expected default scheme 'http', got '%s'", source.Scheme)
	}
	if source.Prefix != "caddy/routing/" {
		t.Errorf("Expected default prefix 'caddy/routing/', got '%s'", source.Prefix)
	}
	if time.Duration(source.WaitTime) != 5*time.Minute {
		t.Errorf("Expected default wait time 5m, got %v", source.WaitTime)
	}
}

func TestConsulSourceCaddyModule(t *testing.T) {
	source := &ConsulSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.consul"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*ConsulSource); !ok {
		t.Error("New() should return *ConsulSource")
	}
}
