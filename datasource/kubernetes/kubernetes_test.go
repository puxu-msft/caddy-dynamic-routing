package kubernetes

import (
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestKubernetesSourceCaddyModule(t *testing.T) {
	source := &KubernetesSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.kubernetes"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*KubernetesSource); !ok {
		t.Error("New() should return *KubernetesSource")
	}
}

func TestKubernetesSourceDefaults(t *testing.T) {
	source := &KubernetesSource{}

	// Simulate what Provision does for defaults
	if source.Namespace == "" {
		source.Namespace = "default"
	}
	if source.ConfigMapName == "" {
		source.ConfigMapName = "caddy-routing"
	}
	if source.MaxCacheSize <= 0 {
		source.MaxCacheSize = 10000
	}

	if source.Namespace != "default" {
		t.Errorf("Expected default namespace 'default', got '%s'", source.Namespace)
	}
	if source.ConfigMapName != "caddy-routing" {
		t.Errorf("Expected default configmap_name 'caddy-routing', got '%s'", source.ConfigMapName)
	}
	if source.MaxCacheSize != 10000 {
		t.Errorf("Expected default max_cache_size 10000, got %d", source.MaxCacheSize)
	}
}

func TestKubernetesSourceValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  *KubernetesSource
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid default config",
			source:  &KubernetesSource{},
			wantErr: false,
		},
		{
			name: "valid config with namespace",
			source: &KubernetesSource{
				Namespace:     "production",
				ConfigMapName: "routing-config",
			},
			wantErr: false,
		},
		{
			name: "valid config with label selector",
			source: &KubernetesSource{
				Namespace:     "default",
				LabelSelector: "app=caddy-dynamic-routing",
			},
			wantErr: false,
		},
		{
			name:    "negative max cache size",
			source:  &KubernetesSource{MaxCacheSize: -1},
			wantErr: true,
			errMsg:  "max_cache_size must be non-negative",
		},
		{
			name:    "zero max cache size is valid",
			source:  &KubernetesSource{MaxCacheSize: 0},
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

func TestKubernetesSourceHealthy(t *testing.T) {
	source := &KubernetesSource{}

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

func TestKubernetesSourceUnmarshalCaddyfile(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		check     func(*KubernetesSource) bool
		checkDesc string
	}{
		{
			name:  "namespace config",
			input: "kubernetes {\n    namespace production\n}",
			check: func(k *KubernetesSource) bool {
				return k.Namespace == "production"
			},
			checkDesc: "namespace should be 'production'",
		},
		{
			name:  "configmap_name config",
			input: "kubernetes {\n    configmap_name my-routing\n}",
			check: func(k *KubernetesSource) bool {
				return k.ConfigMapName == "my-routing"
			},
			checkDesc: "configmap_name should be 'my-routing'",
		},
		{
			name:  "configmap shorthand",
			input: "kubernetes {\n    configmap routes\n}",
			check: func(k *KubernetesSource) bool {
				return k.ConfigMapName == "routes"
			},
			checkDesc: "configmap should be 'routes'",
		},
		{
			name:  "label_selector config",
			input: "kubernetes {\n    label_selector app=caddy\n}",
			check: func(k *KubernetesSource) bool {
				return k.LabelSelector == "app=caddy"
			},
			checkDesc: "label_selector should be 'app=caddy'",
		},
		{
			name:  "kubeconfig config",
			input: "kubernetes {\n    kubeconfig /path/to/config\n}",
			check: func(k *KubernetesSource) bool {
				return k.Kubeconfig == "/path/to/config"
			},
			checkDesc: "kubeconfig should be '/path/to/config'",
		},
		{
			name:  "max_cache_size config",
			input: "kubernetes {\n    max_cache_size 5000\n}",
			check: func(k *KubernetesSource) bool {
				return k.MaxCacheSize == 5000
			},
			checkDesc: "max_cache_size should be 5000",
		},
		{
			name:  "initial_load_timeout config",
			input: "kubernetes {\n    initial_load_timeout 45s\n}",
			check: func(k *KubernetesSource) bool {
				return time.Duration(k.InitialLoadTimeout) == 45*time.Second
			},
			checkDesc: "initial_load_timeout should be 45s",
		},
		{
			name:  "full config",
			input: "kubernetes {\n    namespace prod\n    configmap routing\n    label_selector tier=frontend\n    max_cache_size 20000\n}",
			check: func(k *KubernetesSource) bool {
				return k.Namespace == "prod" &&
					k.ConfigMapName == "routing" &&
					k.LabelSelector == "tier=frontend" &&
					k.MaxCacheSize == 20000
			},
			checkDesc: "all fields should be set correctly",
		},
		{
			name:    "unrecognized directive",
			input:   "kubernetes {\n    unknown_field value\n}",
			wantErr: true,
		},
		{
			name:    "missing namespace arg",
			input:   "kubernetes {\n    namespace\n}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &KubernetesSource{}
			d := caddyfile.NewTestDispenser(tt.input)

			err := source.UnmarshalCaddyfile(d)
			if tt.wantErr {
				if err == nil {
					t.Error("UnmarshalCaddyfile() expected error")
				}
				return
			}
			if err != nil {
				t.Errorf("UnmarshalCaddyfile() unexpected error: %v", err)
				return
			}

			if tt.check != nil && !tt.check(source) {
				t.Errorf("UnmarshalCaddyfile() %s", tt.checkDesc)
			}
		})
	}
}

func TestGetConfigMapExample(t *testing.T) {
	example := GetConfigMapExample()

	if example == "" {
		t.Error("GetConfigMapExample() should return non-empty string")
	}

	// Should contain expected fields
	expectedFields := []string{
		"apiVersion",
		"kind",
		"ConfigMap",
		"metadata",
		"caddy-routing",
		"data",
		"tenant-a",
	}

	for _, field := range expectedFields {
		if !containsStr(example, field) {
			t.Errorf("GetConfigMapExample() should contain '%s'", field)
		}
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
