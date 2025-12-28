package caddyslb

import (
	"fmt"
	"sort"
	"sync"
)

// SelectionPolicyInfo describes a registered selection policy instance for Admin API inspection.
// This is intentionally focused on operational/debugging needs.
type SelectionPolicyInfo struct {
	Name               string `json:"name"`
	ModuleID           string `json:"module_id"`
	Key                string `json:"key,omitempty"`
	InstanceKey        string `json:"instance_key,omitempty"`
	VersionKey         string `json:"version_key,omitempty"`
	DataSourceType     string `json:"data_source_type,omitempty"`
	DataSourceModuleID string `json:"data_source_module_id,omitempty"`
	FallbackPolicy     string `json:"fallback_policy,omitempty"`
}

type selectionPolicyRegistry struct {
	mu      sync.RWMutex
	entries map[string]SelectionPolicyInfo
	counter uint64
}

var globalSelectionPolicyRegistry = &selectionPolicyRegistry{
	entries: make(map[string]SelectionPolicyInfo),
}

func registerSelectionPolicy(info SelectionPolicyInfo) string {
	globalSelectionPolicyRegistry.mu.Lock()
	defer globalSelectionPolicyRegistry.mu.Unlock()

	globalSelectionPolicyRegistry.counter++
	name := fmt.Sprintf("dynamic#%d", globalSelectionPolicyRegistry.counter)
	info.Name = name
	globalSelectionPolicyRegistry.entries[name] = info
	return name
}

func unregisterSelectionPolicy(name string) {
	if name == "" {
		return
	}
	globalSelectionPolicyRegistry.mu.Lock()
	defer globalSelectionPolicyRegistry.mu.Unlock()
	delete(globalSelectionPolicyRegistry.entries, name)
}

func listSelectionPolicies() []SelectionPolicyInfo {
	globalSelectionPolicyRegistry.mu.RLock()
	out := make([]SelectionPolicyInfo, 0, len(globalSelectionPolicyRegistry.entries))
	for _, v := range globalSelectionPolicyRegistry.entries {
		out = append(out, v)
	}
	globalSelectionPolicyRegistry.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
