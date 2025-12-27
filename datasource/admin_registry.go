package datasource

import (
	"fmt"
	"sync"
)

// AdminEntry is a registered data source instance for Admin API inspection.
type AdminEntry struct {
	Name string
	Type string
	DS   AdminInspectable
}

type adminRegistry struct {
	mu       sync.RWMutex
	sources  map[string]AdminEntry
	counters map[string]uint64
}

var globalAdminRegistry = &adminRegistry{
	sources:  make(map[string]AdminEntry),
	counters: make(map[string]uint64),
}

// RegisterAdminSource registers a data source for Admin API inspection and returns its assigned name.
// The name is stable for the lifetime of the instance (until UnregisterAdminSource).
func RegisterAdminSource(ds AdminInspectable) string {
	if ds == nil {
		return ""
	}
	typeName := ds.AdminType()
	if typeName == "" {
		typeName = moduleTypeFromID(string(ds.CaddyModule().ID))
		if typeName == "" {
			typeName = "unknown"
		}
	}

	globalAdminRegistry.mu.Lock()
	defer globalAdminRegistry.mu.Unlock()

	globalAdminRegistry.counters[typeName]++
	name := fmt.Sprintf("%s#%d", typeName, globalAdminRegistry.counters[typeName])
	globalAdminRegistry.sources[name] = AdminEntry{
		Name: name,
		Type: typeName,
		DS:   ds,
	}
	return name
}

// UnregisterAdminSource removes a data source from the Admin registry.
func UnregisterAdminSource(name string) {
	if name == "" {
		return
	}
	globalAdminRegistry.mu.Lock()
	defer globalAdminRegistry.mu.Unlock()
	delete(globalAdminRegistry.sources, name)
}

// ListAdminEntries returns a snapshot list of all registered data sources.
func ListAdminEntries() []AdminEntry {
	globalAdminRegistry.mu.RLock()
	defer globalAdminRegistry.mu.RUnlock()

	out := make([]AdminEntry, 0, len(globalAdminRegistry.sources))
	for _, entry := range globalAdminRegistry.sources {
		out = append(out, entry)
	}
	return out
}

func moduleTypeFromID(id string) string {
	// Extract last segment after '.'
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}
