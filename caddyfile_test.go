package caddyslb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestUnmarshalCaddyfile_Key(t *testing.T) {
	input := `dynamic {
		key {http.request.header.X-Tenant}
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.Key != "{http.request.header.X-Tenant}" {
		t.Errorf("Key = %q, want %q", s.Key, "{http.request.header.X-Tenant}")
	}
}

func TestUnmarshalCaddyfile_KeyMissingArg(t *testing.T) {
	input := `dynamic {
		key
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("Expected error for missing key argument")
	}
}

func TestUnmarshalCaddyfile_Fallback(t *testing.T) {
	tests := []struct {
		name     string
		policy   string
		wantJSON string
	}{
		{"random", "random", `"policy":"random"`},
		{"round_robin", "round_robin", `"policy":"round_robin"`},
		{"least_conn", "least_conn", `"policy":"least_conn"`},
		{"first", "first", `"policy":"first"`},
		{"ip_hash", "ip_hash", `"policy":"ip_hash"`},
		{"uri_hash", "uri_hash", `"policy":"uri_hash"`},
		{"unknown", "unknown_policy", `"policy":"unknown_policy"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `dynamic {
				fallback ` + tt.policy + `
			}`

			d := caddyfile.NewTestDispenser(input)
			var s DynamicSelection
			err := s.UnmarshalCaddyfile(d)
			if err != nil {
				t.Fatalf("UnmarshalCaddyfile error: %v", err)
			}

			if s.FallbackRaw == nil {
				t.Fatal("FallbackRaw is nil")
			}

			jsonStr := string(s.FallbackRaw)
			if !strings.Contains(jsonStr, tt.wantJSON) {
				t.Errorf("FallbackRaw = %s, want to contain %s", jsonStr, tt.wantJSON)
			}
		})
	}
}

func TestUnmarshalCaddyfile_FallbackMissingArg(t *testing.T) {
	input := `dynamic {
		fallback
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("Expected error for missing fallback argument")
	}
}

func TestUnmarshalCaddyfile_UnknownDirective(t *testing.T) {
	input := `dynamic {
		unknown_directive value
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err == nil {
		t.Fatal("Expected error for unknown directive")
	}
	if !strings.Contains(err.Error(), "unrecognized subdirective") {
		t.Errorf("Error = %v, want to contain 'unrecognized subdirective'", err)
	}
}

func TestUnmarshalCaddyfile_EtcdSource(t *testing.T) {
	input := `dynamic {
		key {http.request.header.X-Tenant}
		etcd {
			endpoints localhost:2379 localhost:2380
			prefix /caddy/routing/
		}
		fallback random
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.Key != "{http.request.header.X-Tenant}" {
		t.Errorf("Key = %q, want %q", s.Key, "{http.request.header.X-Tenant}")
	}

	if s.DataSourceRaw == nil {
		t.Fatal("DataSourceRaw is nil")
	}

	jsonStr := string(s.DataSourceRaw)
	if !strings.Contains(jsonStr, `"source":"etcd"`) {
		t.Errorf("DataSourceRaw = %s, want to contain source:etcd", jsonStr)
	}
	if !strings.Contains(jsonStr, "localhost:2379") {
		t.Errorf("DataSourceRaw = %s, want to contain localhost:2379", jsonStr)
	}
}

func TestUnmarshalCaddyfile_RedisSource(t *testing.T) {
	input := `dynamic {
		redis {
			addresses localhost:6379
			prefix routing:
		}
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.DataSourceRaw == nil {
		t.Fatal("DataSourceRaw is nil")
	}

	jsonStr := string(s.DataSourceRaw)
	if !strings.Contains(jsonStr, `"source":"redis"`) {
		t.Errorf("DataSourceRaw = %s, want to contain source:redis", jsonStr)
	}
}

func TestUnmarshalCaddyfile_ConsulSource(t *testing.T) {
	input := `dynamic {
		consul {
			address localhost:8500
			prefix caddy/routing/
		}
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.DataSourceRaw == nil {
		t.Fatal("DataSourceRaw is nil")
	}

	jsonStr := string(s.DataSourceRaw)
	if !strings.Contains(jsonStr, `"source":"consul"`) {
		t.Errorf("DataSourceRaw = %s, want to contain source:consul", jsonStr)
	}
}

func TestUnmarshalCaddyfile_FileSource(t *testing.T) {
	input := `dynamic {
		file {
			path /etc/caddy/routes/
		}
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.DataSourceRaw == nil {
		t.Fatal("DataSourceRaw is nil")
	}

	jsonStr := string(s.DataSourceRaw)
	if !strings.Contains(jsonStr, `"source":"file"`) {
		t.Errorf("DataSourceRaw = %s, want to contain source:file", jsonStr)
	}
}

func TestUnmarshalCaddyfile_HTTPSource(t *testing.T) {
	input := `dynamic {
		http {
			url http://config-service/routes
		}
	}`

	d := caddyfile.NewTestDispenser(input)
	var s DynamicSelection
	err := s.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("UnmarshalCaddyfile error: %v", err)
	}

	if s.DataSourceRaw == nil {
		t.Fatal("DataSourceRaw is nil")
	}

	jsonStr := string(s.DataSourceRaw)
	if !strings.Contains(jsonStr, `"source":"http"`) {
		t.Errorf("DataSourceRaw = %s, want to contain source:http", jsonStr)
	}
}

func TestMarshalJSON(t *testing.T) {
	s := DynamicSelection{
		Key: "{http.request.header.X-Tenant}",
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"policy":"dynamic"`) {
		t.Errorf("JSON = %s, want to contain policy:dynamic", jsonStr)
	}
	if !strings.Contains(jsonStr, `"key":"{http.request.header.X-Tenant}"`) {
		t.Errorf("JSON = %s, want to contain key", jsonStr)
	}
}

func TestCreateFallbackPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		isNil  bool
	}{
		{"random", "random", false},
		{"round_robin", "round_robin", false},
		{"least_conn", "least_conn", false},
		{"first", "first", false},
		{"ip_hash", "ip_hash", false},
		{"uri_hash", "uri_hash", false},
		{"unknown", "custom_policy", false}, // Returns empty map
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createFallbackPolicy(tt.policy)
			if tt.isNil && result != nil {
				t.Errorf("createFallbackPolicy(%s) = %v, want nil", tt.policy, result)
			}
			if !tt.isNil && result == nil {
				t.Errorf("createFallbackPolicy(%s) = nil, want non-nil", tt.policy)
			}
		})
	}
}
