package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

func TestHTTPSourceIntegration(t *testing.T) {
	// Create a test HTTP server
	routes := map[string]string{
		"tenant-a": "backend-a:8080",
		"tenant-b": `{
			"rules": [
				{
					"match": {"http.request.header.X-Version": "v2"},
					"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
					"priority": 10
				}
			],
			"fallback": "random"
		}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract key from path
		key := r.URL.Path[1:] // Remove leading slash

		if value, ok := routes[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// If it's already JSON, write as-is; otherwise wrap as simple upstream
			if json.Valid([]byte(value)) {
				if _, err := w.Write([]byte(value)); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				if err := json.NewEncoder(w).Encode(value); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create and setup the data source manually (without Caddy context)
	source := &HTTPSource{
		URL:            server.URL,
		Method:         "GET",
		Timeout:        caddy.Duration(5 * time.Second),
		CacheTTL:       caddy.Duration(1 * time.Minute),
		HealthEndpoint: server.URL,
		MaxCacheSize:   1000,
		logger:         zap.NewNop(),
		cache:          cache.NewRouteCache(1000),
	}

	// Create HTTP client
	source.client = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: source.TLSSkipVerify,
			},
		},
		Timeout: time.Duration(source.Timeout),
	}
	source.healthy.Store(true)

	ctx := context.Background()

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

	t.Run("Cache works", func(t *testing.T) {
		// Get should use cache
		config1, _ := source.Get(ctx, "tenant-a")
		config2, _ := source.Get(ctx, "tenant-a")

		if config1 != config2 {
			t.Error("Expected same cached config")
		}
	})
}

func TestHTTPSourceWithHeaders(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`"backend:8080"`)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	source := &HTTPSource{
		URL:    server.URL,
		Method: "GET",
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Timeout:      caddy.Duration(5 * time.Second),
		CacheTTL:     caddy.Duration(1 * time.Minute),
		MaxCacheSize: 1000,
		logger:       zap.NewNop(),
		cache:        cache.NewRouteCache(1000),
	}

	// Create HTTP client
	source.client = &http.Client{
		Timeout: time.Duration(source.Timeout),
	}
	source.healthy.Store(true)

	ctx := context.Background()
	if _, err := source.Get(ctx, "test"); err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if receivedAuth != "Bearer test-token" {
		t.Errorf("Expected Authorization header 'Bearer test-token', got '%s'", receivedAuth)
	}
}

func TestHTTPSourceDefaults(t *testing.T) {
	source := &HTTPSource{}

	// Simulate what Provision does for defaults
	if source.Method == "" {
		source.Method = "GET"
	}
	if source.Timeout == 0 {
		source.Timeout = caddy.Duration(5 * time.Second)
	}
	if source.CacheTTL == 0 {
		source.CacheTTL = caddy.Duration(1 * time.Minute)
	}

	if source.Method != "GET" {
		t.Errorf("Expected default method 'GET', got '%s'", source.Method)
	}
	if time.Duration(source.Timeout) != 5*time.Second {
		t.Errorf("Expected default timeout 5s, got %v", source.Timeout)
	}
	if time.Duration(source.CacheTTL) != 1*time.Minute {
		t.Errorf("Expected default cache TTL 1m, got %v", source.CacheTTL)
	}
}

func TestHTTPSourceCaddyModule(t *testing.T) {
	source := &HTTPSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.http"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*HTTPSource); !ok {
		t.Error("New() should return *HTTPSource")
	}
}

func TestHTTPSourceUnhealthy(t *testing.T) {
	// Create a server that fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	source := &HTTPSource{
		URL:            server.URL,
		Method:         "GET",
		HealthEndpoint: server.URL + "/health",
		Timeout:        caddy.Duration(1 * time.Second),
		CacheTTL:       caddy.Duration(1 * time.Minute),
		logger:         zap.NewNop(),
	}

	// Create HTTP client
	source.client = &http.Client{
		Timeout: time.Duration(source.Timeout),
	}

	// Health check should fail
	err := source.healthCheck()
	if err == nil {
		t.Error("Expected health check to fail with 500 response")
	}
}
