package caddyslb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
)

// mockUpstreamDataSource implements datasource.DataSource for testing.
type mockUpstreamDataSource struct {
	configs map[string]*datasource.RouteConfig
	healthy bool
}

func (m *mockUpstreamDataSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "test.mock"}
}

func (m *mockUpstreamDataSource) Provision(ctx caddy.Context) error {
	return nil
}

func (m *mockUpstreamDataSource) Cleanup() error {
	return nil
}

func (m *mockUpstreamDataSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	if config, ok := m.configs[key]; ok {
		return config, nil
	}
	return nil, nil
}

func (m *mockUpstreamDataSource) Healthy() bool {
	return m.healthy
}

func TestDynamicUpstreams_GetUpstreams(t *testing.T) {
	tests := []struct {
		name           string
		configs        map[string]*datasource.RouteConfig
		defaultKey     string
		expectedCount  int
		expectedAddrs  []string
	}{
		{
			name: "simple upstream",
			configs: map[string]*datasource.RouteConfig{
				"_default_": {
					Upstream: "backend:8080",
				},
			},
			expectedCount: 1,
			expectedAddrs: []string{"backend:8080"},
		},
		{
			name: "multiple upstreams from rules",
			configs: map[string]*datasource.RouteConfig{
				"_default_": {
					Rules: []datasource.Rule{
						{
							Upstreams: []datasource.WeightedUpstream{
								{Address: "backend1:8080"},
								{Address: "backend2:8080"},
							},
						},
						{
							Upstreams: []datasource.WeightedUpstream{
								{Address: "backend3:8080"},
							},
						},
					},
				},
			},
			expectedCount: 3,
			expectedAddrs: []string{"backend1:8080", "backend2:8080", "backend3:8080"},
		},
		{
			name: "deduplicated upstreams",
			configs: map[string]*datasource.RouteConfig{
				"_default_": {
					Upstream: "backend:8080",
					Rules: []datasource.Rule{
						{
							Upstreams: []datasource.WeightedUpstream{
								{Address: "backend:8080"}, // duplicate
								{Address: "backend2:8080"},
							},
						},
					},
				},
			},
			expectedCount: 2,
			expectedAddrs: []string{"backend:8080", "backend2:8080"},
		},
		{
			name: "with default key",
			configs: map[string]*datasource.RouteConfig{
				"my-key": {
					Upstream: "backend:9090",
				},
			},
			defaultKey:    "my-key",
			expectedCount: 1,
			expectedAddrs: []string{"backend:9090"},
		},
		{
			name:          "no config",
			configs:       map[string]*datasource.RouteConfig{},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DynamicUpstreams{
				DefaultKey:      tt.defaultKey,
				RefreshInterval: caddy.Duration(time.Hour), // Long interval to test caching
				dataSource: &mockUpstreamDataSource{
					configs: tt.configs,
					healthy: true,
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)

			upstreams, err := d.GetUpstreams(req)
			if err != nil {
				t.Fatalf("GetUpstreams returned error: %v", err)
			}

			if len(upstreams) != tt.expectedCount {
				t.Errorf("Expected %d upstreams, got %d", tt.expectedCount, len(upstreams))
			}

			for i, expectedAddr := range tt.expectedAddrs {
				if i >= len(upstreams) {
					break
				}
				if upstreams[i].Dial != expectedAddr {
					t.Errorf("Upstream[%d]: expected %s, got %s", i, expectedAddr, upstreams[i].Dial)
				}
			}
		})
	}
}

func TestDynamicUpstreams_Caching(t *testing.T) {
	callCount := 0
	d := &DynamicUpstreams{
		RefreshInterval: caddy.Duration(100 * time.Millisecond),
		dataSource: &countingDataSource{
			config: &datasource.RouteConfig{
				Upstream: "backend:8080",
			},
			callCount: &callCount,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// First call
	_, err := d.GetUpstreams(req)
	if err != nil {
		t.Fatalf("First GetUpstreams returned error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// Second call should use cache
	_, err = d.GetUpstreams(req)
	if err != nil {
		t.Fatalf("Second GetUpstreams returned error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call (cached), got %d", callCount)
	}

	// Wait for cache to expire
	time.Sleep(150 * time.Millisecond)

	// Third call should refresh
	_, err = d.GetUpstreams(req)
	if err != nil {
		t.Fatalf("Third GetUpstreams returned error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 calls after cache expiry, got %d", callCount)
	}
}

type countingDataSource struct {
	config    *datasource.RouteConfig
	callCount *int
}

func (c *countingDataSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "test.counting"}
}

func (c *countingDataSource) Provision(ctx caddy.Context) error {
	return nil
}

func (c *countingDataSource) Cleanup() error {
	return nil
}

func (c *countingDataSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	*c.callCount++
	return c.config, nil
}

func (c *countingDataSource) Healthy() bool {
	return true
}

func TestDynamicUpstreams_dataSourceType(t *testing.T) {
	tests := []struct {
		name     string
		moduleID string
		expected string
	}{
		{
			name:     "etcd",
			moduleID: "http.reverse_proxy.selection_policies.dynamic.sources.etcd",
			expected: "etcd",
		},
		{
			name:     "simple",
			moduleID: "simple",
			expected: "simple",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DynamicUpstreams{
				dataSource: &mockModuleDataSource{moduleID: tt.moduleID},
			}

			result := d.dataSourceType()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

type mockModuleDataSource struct {
	moduleID string
}

func (m *mockModuleDataSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: caddy.ModuleID(m.moduleID)}
}

func (m *mockModuleDataSource) Provision(ctx caddy.Context) error {
	return nil
}

func (m *mockModuleDataSource) Cleanup() error {
	return nil
}

func (m *mockModuleDataSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	return nil, nil
}

func (m *mockModuleDataSource) Healthy() bool {
	return true
}

func TestDynamicUpstreams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *DynamicUpstreams
		wantErr bool
	}{
		{
			name: "valid",
			d: &DynamicUpstreams{
				RefreshInterval: caddy.Duration(5 * time.Second),
			},
			wantErr: false,
		},
		{
			name: "negative refresh interval",
			d: &DynamicUpstreams{
				RefreshInterval: caddy.Duration(-1 * time.Second),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.d.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
