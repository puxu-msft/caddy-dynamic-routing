package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestFileSourceDirectory(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "caddy-dynamic-routing-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("Failed to remove temp dir: %v", err)
		}
	})

	// Create test files
	if err := os.WriteFile(filepath.Join(tmpDir, "tenant-a"), []byte("backend-a:8080"), 0644); err != nil {
		t.Fatalf("Failed to write tenant-a file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "tenant-b.json"), []byte(`{
		"rules": [
			{
				"match": {"http.request.header.X-Version": "v2"},
				"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
				"priority": 10
			}
		],
		"fallback": "random"
	}`), 0644); err != nil {
		t.Fatalf("Failed to write tenant-b.json file: %v", err)
	}

	// Create and test the data source without Caddy context
	watchEnabled := false
	source := &FileSource{
		Path:   tmpDir,
		Watch:  &watchEnabled,
		Format: "auto",
		logger: zap.NewNop(),
	}
	source.isDir = true
	source.healthy.Store(true)

	// Load manually
	if err := source.loadAll(); err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

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
}

func TestFileSourceSingleFile(t *testing.T) {
	// Create temp file
	tmpFile, err := os.CreateTemp("", "caddy-dynamic-routing-test-*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString(`{
		"tenant-a": "backend-a:8080",
		"tenant-b": {
			"rules": [
				{
					"match": {"http.request.header.X-Version": "v2"},
					"upstreams": [{"address": "backend-v2:8080", "weight": 100}],
					"priority": 10
				}
			],
			"fallback": "random"
		}
	}`); err != nil {
		if closeErr := tmpFile.Close(); closeErr != nil {
			t.Errorf("Failed to close temp file after write error: %v", closeErr)
		}
		t.Fatalf("Failed to write temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Errorf("Failed to remove temp file: %v", err)
		}
	})

	// Create and test the data source without Caddy context
	watchEnabled := false
	source := &FileSource{
		Path:   tmpFile.Name(),
		Watch:  &watchEnabled,
		Format: "auto",
		logger: zap.NewNop(),
	}
	source.isDir = false
	source.healthy.Store(true)

	// Load manually
	if err := source.loadAll(); err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

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
}

func TestFileSourceReload(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "caddy-dynamic-routing-test-reload-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("Failed to remove temp dir: %v", err)
		}
	})

	// Create initial file
	if err := os.WriteFile(filepath.Join(tmpDir, "tenant-a"), []byte("backend-a:8080"), 0644); err != nil {
		t.Fatalf("Failed to write tenant-a file: %v", err)
	}

	// Create data source
	watchEnabled := false
	source := &FileSource{
		Path:   tmpDir,
		Watch:  &watchEnabled,
		Format: "auto",
		logger: zap.NewNop(),
	}
	source.isDir = true
	source.healthy.Store(true)

	// Load initially
	if err := source.loadAll(); err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	ctx := context.Background()

	// Verify initial config
	config, _ := source.Get(ctx, "tenant-a")
	if config == nil || config.Upstream != "backend-a:8080" {
		t.Fatal("Initial config not loaded")
	}

	// Update file
	if err := os.WriteFile(filepath.Join(tmpDir, "tenant-a"), []byte("backend-a-updated:8080"), 0644); err != nil {
		t.Fatalf("Failed to update tenant-a file: %v", err)
	}

	// Manually reload
	if err := source.loadAll(); err != nil {
		t.Fatalf("loadAll failed after update: %v", err)
	}

	// Verify updated config
	config, _ = source.Get(ctx, "tenant-a")
	if config == nil {
		t.Fatal("Config should exist after update")
	}
	if config.Upstream != "backend-a-updated:8080" {
		t.Errorf("Expected updated upstream, got %s", config.Upstream)
	}
}

func TestFileSourceCaddyModule(t *testing.T) {
	source := &FileSource{}
	info := source.CaddyModule()

	expectedID := "http.reverse_proxy.selection_policies.dynamic.sources.file"
	if string(info.ID) != expectedID {
		t.Errorf("Expected module ID '%s', got '%s'", expectedID, info.ID)
	}

	newModule := info.New()
	if _, ok := newModule.(*FileSource); !ok {
		t.Error("New() should return *FileSource")
	}
}
