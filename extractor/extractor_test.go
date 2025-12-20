package extractor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestNewFromExpression(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantNil bool
	}{
		{"empty expression", "", true},
		{"simple placeholder", "{http.request.header.X-Tenant}", false},
		{"shortcut header", "{header.X-Tenant}", false},
		{"shortcut cookie", "{cookie.session}", false},
		{"shortcut query", "{query.tenant}", false},
		{"shortcut host", "{host}", false},
		{"composite expression", "{header.X-Org}-{cookie.region}", false},
		{"literal and placeholder", "prefix-{header.X-Tenant}-suffix", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext, err := NewFromExpression(tt.expr)
			if err != nil {
				t.Fatalf("NewFromExpression failed: %v", err)
			}
			if tt.wantNil && ext != nil {
				t.Error("Expected nil extractor")
			}
			if !tt.wantNil && ext == nil {
				t.Error("Expected non-nil extractor")
			}
		})
	}
}

func TestExpandShortcuts(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"{header.X-Tenant}", "{http.request.header.X-Tenant}"},
		{"{cookie.session}", "{http.request.cookie.session}"},
		{"{query.id}", "{http.request.uri.query.id}"},
		{"{host}", "{http.request.host}"},
		{"{method}", "{http.request.method}"},
		{"{path}", "{http.request.uri.path}"},
		// Already expanded
		{"{http.request.header.X-Test}", "{http.request.header.X-Test}"},
		// Mixed
		{"{header.X-Org}-{cookie.region}", "{http.request.header.X-Org}-{http.request.cookie.region}"},
		// Literal text preserved
		{"prefix-{header.X-Tenant}-suffix", "prefix-{http.request.header.X-Tenant}-suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := expandShortcuts(tt.input)
			if result != tt.expected {
				t.Errorf("expandShortcuts(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseExpression(t *testing.T) {
	tests := []struct {
		expr          string
		expectedParts int
	}{
		{"{http.request.header.X-Tenant}", 1},
		{"prefix-{http.request.header.X-Tenant}", 2},
		{"{http.request.header.X-Tenant}-suffix", 2},
		{"prefix-{http.request.header.X-Tenant}-suffix", 3},
		{"{http.request.header.X-Org}-{http.request.cookie.region}", 3},
		{"literal-only", 1},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			parts := parseExpression(tt.expr)
			if len(parts) != tt.expectedParts {
				t.Errorf("parseExpression(%q) returned %d parts, want %d", tt.expr, len(parts), tt.expectedParts)
			}
		})
	}
}

func TestPlaceholderExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		headers  map[string]string
		cookies  map[string]string
		query    map[string]string
		expected string
	}{
		{
			name:     "extract header",
			expr:     "{header.X-Tenant}",
			headers:  map[string]string{"X-Tenant": "tenant-abc"},
			expected: "tenant-abc",
		},
		{
			name:     "extract missing header",
			expr:     "{header.X-Missing}",
			headers:  map[string]string{},
			expected: "",
		},
		{
			name:     "extract query param",
			expr:     "{query.tenant}",
			query:    map[string]string{"tenant": "tenant-xyz"},
			expected: "tenant-xyz",
		},
		{
			name:     "composite expression",
			expr:     "{header.X-Org}-{header.X-Region}",
			headers:  map[string]string{"X-Org": "acme", "X-Region": "us-west"},
			expected: "acme-us-west",
		},
		{
			name:     "composite with missing part",
			expr:     "{header.X-Org}-{header.X-Region}",
			headers:  map[string]string{"X-Org": "acme"},
			expected: "", // If any part is missing, return empty
		},
		{
			name:     "literal prefix and suffix",
			expr:     "route-{header.X-Tenant}-v1",
			headers:  map[string]string{"X-Tenant": "abc"},
			expected: "route-abc-v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext, err := NewFromExpression(tt.expr)
			if err != nil {
				t.Fatalf("NewFromExpression failed: %v", err)
			}
			if ext == nil {
				t.Fatal("Expected non-nil extractor")
			}

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			for k, v := range tt.cookies {
				req.AddCookie(&http.Cookie{Name: k, Value: v})
			}
			if len(tt.query) > 0 {
				q := req.URL.Query()
				for k, v := range tt.query {
					q.Set(k, v)
				}
				req.URL.RawQuery = q.Encode()
			}

			// Create replacer
			repl := caddy.NewReplacer()
			repl.Set("http.request.host", req.Host)
			repl.Set("http.request.method", req.Method)
			repl.Set("http.request.uri.path", req.URL.Path)
			for k, v := range tt.headers {
				repl.Set("http.request.header."+k, v)
			}
			for k, v := range tt.query {
				repl.Set("http.request.uri.query."+k, v)
			}

			result := ext.Extract(req, repl)
			if result != tt.expected {
				t.Errorf("Extract() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPlaceholderExtractor_ExtractNilReplacer(t *testing.T) {
	ext, err := NewFromExpression("{header.X-Tenant}")
	if err != nil {
		t.Fatalf("NewFromExpression failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	result := ext.Extract(req, nil)

	if result != "" {
		t.Errorf("Extract with nil replacer should return empty, got %q", result)
	}
}

func TestPlaceholderExtractor_String(t *testing.T) {
	ext, _ := NewFromExpression("{header.X-Tenant}")
	pe := ext.(*PlaceholderExtractor)

	// String should return the expanded expression
	if pe.String() != "{http.request.header.X-Tenant}" {
		t.Errorf("String() = %q, want %q", pe.String(), "{http.request.header.X-Tenant}")
	}
}
