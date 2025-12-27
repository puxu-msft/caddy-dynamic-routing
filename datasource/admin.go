package datasource

// AdminRouteInfo is a normalized, admin-facing summary of a cached route config.
// It is intentionally simple and stable for the Admin API.
type AdminRouteInfo struct {
	Key       string   `json:"key"`
	Upstream  string   `json:"upstream,omitempty"`
	Upstreams []string `json:"upstreams,omitempty"`
	RuleCount int      `json:"rule_count"`
}

// AdminCacheStats is a snapshot of a data source's cache behavior.
type AdminCacheStats struct {
	Entries      int     `json:"entries"`
	MaxSize      int     `json:"max_size"`
	Hits         uint64  `json:"hits"`
	Misses       uint64  `json:"misses"`
	NegativeHits uint64  `json:"negative_hits"`
	HitRate      float64 `json:"hit_rate"`
}

// AdminInspectable is implemented by data sources that can be inspected via the Admin API.
//
// Contract:
// - AdminListRoutes should be derived from local state (e.g., cache) and must be fast.
// - AdminClearCache should clear all local caches (including negative caches).
// - AdminLastError should be best-effort and may be empty.
type AdminInspectable interface {
	DataSource

	AdminType() string
	AdminListRoutes() []AdminRouteInfo
	AdminCacheStats() AdminCacheStats
	AdminClearCache()
	AdminLastError() string
}
