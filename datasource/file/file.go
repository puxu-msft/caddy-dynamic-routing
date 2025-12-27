// Package file provides a file-based data source for dynamic routing configuration.
package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/metrics"
)

func init() {
	caddy.RegisterModule(&FileSource{})
}

// FileSource implements datasource.DataSource using local files as the backend.
// Supports two modes:
// 1. Directory mode: each file in the directory represents a route (filename = key)
// 2. Single file mode: one JSON/YAML file containing all routes
type FileSource struct {
	// Path is the path to the configuration file or directory.
	Path string `json:"path,omitempty"`

	// Format specifies the file format: "json", "yaml", or "auto" (default).
	// In auto mode, format is detected from file extension.
	Format string `json:"format,omitempty"`

	// Watch enables file watching for automatic reloads.
	// Default: true
	Watch *bool `json:"watch,omitempty"`

	// PollInterval is the interval for polling file changes (fallback if fsnotify fails).
	// Default: 0 (disabled)
	PollInterval caddy.Duration `json:"poll_interval,omitempty"`

	// Internal state
	cache       sync.Map // map[string]*datasource.RouteConfig
	healthy     atomic.Bool
	logger      *zap.Logger
	cancelWatch context.CancelFunc
	watchWg     sync.WaitGroup
	isDir       bool

	// Admin / diagnostics
	adminName string
	lastError atomic.Value // string
	hits      atomic.Uint64
	misses    atomic.Uint64
}

// CaddyModule returns the Caddy module information.
func (*FileSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.file",
		New: func() caddy.Module { return new(FileSource) },
	}
}

// Provision sets up the file data source.
func (f *FileSource) Provision(ctx caddy.Context) error {
	f.logger = ctx.Logger()

	// Set defaults
	if f.Format == "" {
		f.Format = "auto"
	}
	if f.Watch == nil {
		enabled := true
		f.Watch = &enabled
	}

	if f.Path == "" {
		return fmt.Errorf("path is required")
	}

	// Check if path exists and determine if it's a directory
	info, err := os.Stat(f.Path)
	if err != nil {
		return fmt.Errorf("path error: %v", err)
	}
	f.isDir = info.IsDir()

	// Initial load
	if err := f.loadAll(); err != nil {
		f.logger.Warn("initial file load failed", zap.Error(err))
		f.lastError.Store(err.Error())
		f.healthy.Store(false)
	} else {
		f.healthy.Store(true)
		f.lastError.Store("")
	}

	// Start watcher if enabled
	if *f.Watch {
		watchCtx, cancel := context.WithCancel(context.Background())
		f.cancelWatch = cancel
		f.watchWg.Add(1)
		go f.watchLoop(watchCtx)
	}

	f.logger.Info("file data source provisioned",
		zap.String("path", f.Path),
		zap.Bool("is_dir", f.isDir),
		zap.Bool("watch", *f.Watch),
	)

	// Register for Admin API inspection
	f.adminName = datasource.RegisterAdminSource(f)

	return nil
}

// Cleanup releases resources.
func (f *FileSource) Cleanup() error {
	datasource.UnregisterAdminSource(f.adminName)
	f.adminName = ""

	if f.cancelWatch != nil {
		f.cancelWatch()
	}
	f.watchWg.Wait()
	return nil
}

// Get retrieves the route configuration for the given key.
func (f *FileSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	if cached, ok := f.cache.Load(key); ok {
		f.hits.Add(1)
		return cached.(*datasource.RouteConfig), nil
	}
	f.misses.Add(1)
	return nil, nil
}

// AdminType returns the short type name for Admin API.
func (*FileSource) AdminType() string { return "file" }

// AdminLastError returns the most recent error message (best-effort).
func (f *FileSource) AdminLastError() string {
	if v := f.lastError.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AdminListRoutes returns a snapshot of in-memory cached routes.
func (f *FileSource) AdminListRoutes() []datasource.AdminRouteInfo {
	routes := make([]datasource.AdminRouteInfo, 0)
	f.cache.Range(func(k, v any) bool {
		key, ok := k.(string)
		if !ok {
			return true
		}
		cfg, ok := v.(*datasource.RouteConfig)
		if !ok {
			return true
		}
		routes = append(routes, datasource.SummarizeRouteConfig(key, cfg))
		return true
	})
	return routes
}

// AdminCacheStats returns a snapshot of in-memory cache stats.
func (f *FileSource) AdminCacheStats() datasource.AdminCacheStats {
	entries := 0
	f.cache.Range(func(_, _ any) bool {
		entries++
		return true
	})
	h := f.hits.Load()
	m := f.misses.Load()
	total := h + m
	hr := 0.0
	if total > 0 {
		hr = float64(h) / float64(total)
	}
	return datasource.AdminCacheStats{
		Entries: entries,
		MaxSize: 0,
		Hits:    h,
		Misses:  m,
		HitRate: hr,
	}
}

// AdminClearCache clears all in-memory state.
func (f *FileSource) AdminClearCache() {
	f.cache.Range(func(k, _ any) bool {
		f.cache.Delete(k)
		return true
	})
	f.hits.Store(0)
	f.misses.Store(0)
}

// Healthy returns true if the file source is healthy.
func (f *FileSource) Healthy() bool {
	return f.healthy.Load()
}

// loadAll loads all configurations from the file or directory.
func (f *FileSource) loadAll() error {
	if f.isDir {
		return f.loadDirectory()
	}
	return f.loadSingleFile()
}

// loadDirectory loads configurations from a directory (one file per route).
func (f *FileSource) loadDirectory() error {
	entries, err := os.ReadDir(f.Path)
	if err != nil {
		return fmt.Errorf("reading directory: %v", err)
	}

	// Track loaded keys to detect deletions
	loadedKeys := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Skip hidden files and non-config files
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Determine key from filename (without extension)
		key := strings.TrimSuffix(name, filepath.Ext(name))

		filePath := filepath.Join(f.Path, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			f.logger.Warn("failed to read file", zap.String("file", filePath), zap.Error(err))
			continue
		}

		config, err := datasource.ParseRouteConfig(data)
		if err != nil {
			metrics.RecordRouteConfigParseError(f.AdminType())
			f.logger.Warn("failed to parse config", zap.String("file", filePath), zap.Error(err))
			continue
		}

		if config != nil {
			f.cache.Store(key, config)
			loadedKeys[key] = true
		}
	}

	// Remove deleted entries
	f.cache.Range(func(k, v interface{}) bool {
		key, ok := k.(string)
		if !ok {
			return true
		}
		if !loadedKeys[key] {
			f.cache.Delete(key)
			f.logger.Info("route config deleted", zap.String("key", key))
		}
		return true
	})

	f.logger.Info("directory load completed", zap.Int("count", len(loadedKeys)))
	return nil
}

// loadSingleFile loads configurations from a single JSON file.
func (f *FileSource) loadSingleFile() error {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return fmt.Errorf("reading file: %v", err)
	}

	// Parse as map of key -> config
	var routes map[string]json.RawMessage
	if err := json.Unmarshal(data, &routes); err != nil {
		return fmt.Errorf("parsing JSON: %v", err)
	}

	// Track loaded keys
	loadedKeys := make(map[string]bool)

	for key, raw := range routes {
		config, err := datasource.ParseRouteConfig(raw)
		if err != nil {
			metrics.RecordRouteConfigParseError(f.AdminType())
			f.logger.Warn("failed to parse config", zap.String("key", key), zap.Error(err))
			continue
		}

		if config != nil {
			f.cache.Store(key, config)
			loadedKeys[key] = true
		}
	}

	// Remove deleted entries
	f.cache.Range(func(k, v interface{}) bool {
		key, ok := k.(string)
		if !ok {
			return true
		}
		if !loadedKeys[key] {
			f.cache.Delete(key)
		}
		return true
	})

	f.logger.Info("single file load completed", zap.Int("count", len(loadedKeys)))
	return nil
}

// watchLoop watches for file changes.
func (f *FileSource) watchLoop(ctx context.Context) {
	defer f.watchWg.Done()

	// Try fsnotify first
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		f.logger.Warn("fsnotify not available, falling back to polling", zap.Error(err))
		f.pollLoop(ctx)
		return
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			f.logger.Debug("failed to close fsnotify watcher", zap.Error(err))
		}
	}()

	// Add path to watcher
	if err := watcher.Add(f.Path); err != nil {
		f.logger.Warn("failed to watch path, falling back to polling", zap.Error(err))
		f.pollLoop(ctx)
		return
	}

	f.logger.Debug("file watch started", zap.String("path", f.Path))

	// Debounce timer
	var debounceTimer *time.Timer
	debounceDuration := 100 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Handle relevant events
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				// Debounce multiple events
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDuration, func() {
					if err := f.loadAll(); err != nil {
						f.logger.Warn("reload failed", zap.Error(err))
					} else {
						f.logger.Info("configuration reloaded", zap.String("trigger", event.Name))
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			f.logger.Warn("watcher error", zap.Error(err))
		}
	}
}

// pollLoop polls for file changes at regular intervals.
func (f *FileSource) pollLoop(ctx context.Context) {
	interval := time.Duration(f.PollInterval)
	if interval == 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	f.logger.Debug("polling started", zap.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := f.loadAll(); err != nil {
				f.logger.Warn("poll reload failed", zap.Error(err))
			}
		}
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (f *FileSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "path":
				if !d.NextArg() {
					return d.ArgErr()
				}
				f.Path = d.Val()

			case "format":
				if !d.NextArg() {
					return d.ArgErr()
				}
				f.Format = d.Val()

			case "watch":
				var watch bool
				if d.NextArg() {
					switch d.Val() {
					case "true", "on", "yes":
						watch = true
					case "false", "off", "no":
						watch = false
					default:
						return d.Errf("invalid watch value: %s", d.Val())
					}
				} else {
					watch = true
				}
				f.Watch = &watch

			case "poll_interval":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid poll_interval: %v", err)
				}
				f.PollInterval = caddy.Duration(dur)

			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}

	return nil
}

// Validate implements caddy.Validator.
func (f *FileSource) Validate() error {
	if f.Path == "" {
		return fmt.Errorf("path is required")
	}
	if f.Format != "" && f.Format != "auto" && f.Format != "json" && f.Format != "yaml" {
		return fmt.Errorf("format must be 'auto', 'json', or 'yaml'")
	}
	if f.PollInterval < 0 {
		return fmt.Errorf("poll_interval must be non-negative")
	}
	return nil
}

// Interface guards
var (
	_ caddy.Module          = (*FileSource)(nil)
	_ caddy.Provisioner     = (*FileSource)(nil)
	_ caddy.CleanerUpper    = (*FileSource)(nil)
	_ caddy.Validator       = (*FileSource)(nil)
	_ datasource.DataSource = (*FileSource)(nil)
	_ caddyfile.Unmarshaler = (*FileSource)(nil)
)
