// Package kubernetes provides a Kubernetes ConfigMap data source for dynamic routing configuration.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/cache"
)

func init() {
	caddy.RegisterModule(&KubernetesSource{})
}

// KubernetesSource implements datasource.DataSource using Kubernetes ConfigMaps.
type KubernetesSource struct {
	// Namespace is the Kubernetes namespace to watch ConfigMaps in.
	// Default is "default".
	Namespace string `json:"namespace,omitempty"`

	// ConfigMapName is the name of the ConfigMap containing routing configurations.
	// Default is "caddy-routing".
	ConfigMapName string `json:"configmap_name,omitempty"`

	// LabelSelector is an optional label selector to filter ConfigMaps.
	// If empty, only ConfigMapName is used.
	LabelSelector string `json:"label_selector,omitempty"`

	// Kubeconfig is the path to the kubeconfig file.
	// If empty, in-cluster configuration is used.
	Kubeconfig string `json:"kubeconfig,omitempty"`

	// MaxCacheSize is the maximum number of entries in the local cache.
	// Default is 10000.
	MaxCacheSize int `json:"max_cache_size,omitempty"`

	// internal fields
	client    kubernetes.Interface
	cache     *cache.RouteCache
	healthy   atomic.Bool
	logger    *zap.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	watcherWg sync.WaitGroup
	sfGroup   singleflight.Group // Coalesce concurrent requests for same key
}

// CaddyModule returns the Caddy module information.
func (*KubernetesSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.selection_policies.dynamic.sources.kubernetes",
		New: func() caddy.Module { return new(KubernetesSource) },
	}
}

// Provision sets up the Kubernetes data source.
func (k *KubernetesSource) Provision(ctx caddy.Context) error {
	k.logger = ctx.Logger()

	// Set defaults
	if k.Namespace == "" {
		k.Namespace = "default"
	}
	if k.ConfigMapName == "" {
		k.ConfigMapName = "caddy-routing"
	}
	if k.MaxCacheSize <= 0 {
		k.MaxCacheSize = 10000
	}

	// Initialize cache
	k.cache = cache.NewRouteCache(k.MaxCacheSize)

	// Create context for watchers
	k.ctx, k.cancel = context.WithCancel(context.Background())

	// Create Kubernetes client
	var config *rest.Config
	var err error

	if k.Kubeconfig != "" {
		// Use kubeconfig file
		config, err = clientcmd.BuildConfigFromFlags("", k.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to load kubeconfig: %w", err)
		}
	} else {
		// Use in-cluster configuration
		config, err = rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	}

	k.client, err = kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Initial load
	if err := k.initialLoad(); err != nil {
		k.logger.Warn("initial kubernetes load failed", zap.Error(err))
		k.healthy.Store(false)
	} else {
		k.healthy.Store(true)
	}

	// Start watcher
	k.watcherWg.Add(1)
	go k.watchConfigMaps()

	k.logger.Info("kubernetes data source initialized",
		zap.String("namespace", k.Namespace),
		zap.String("configmap", k.ConfigMapName))

	return nil
}

// Cleanup releases Kubernetes resources.
func (k *KubernetesSource) Cleanup() error {
	if k.cancel != nil {
		k.cancel()
	}
	k.watcherWg.Wait()
	k.logger.Info("kubernetes data source cleaned up")
	return nil
}

// Get retrieves routing configuration for the given key.
// Uses singleflight to coalesce concurrent requests for the same key.
func (k *KubernetesSource) Get(ctx context.Context, key string) (*datasource.RouteConfig, error) {
	// Check cache first
	if config := k.cache.Get(key); config != nil {
		return config, nil
	}

	// Check negative cache
	if k.cache.IsNegativeCached(key) {
		return nil, nil
	}

	// If unhealthy, don't try to fetch from Kubernetes
	if !k.healthy.Load() {
		return nil, nil
	}

	// Use singleflight to coalesce concurrent requests for the same key
	result, err, _ := k.sfGroup.Do(key, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if config := k.cache.Get(key); config != nil {
			return config, nil
		}

		// Fetch from Kubernetes
		cm, err := k.client.CoreV1().ConfigMaps(k.Namespace).Get(ctx, k.ConfigMapName, metav1.GetOptions{})
		if err != nil {
			k.healthy.Store(false)
			return nil, fmt.Errorf("failed to get configmap: %w", err)
		}

		data, ok := cm.Data[key]
		if !ok {
			// Cache negative result to avoid repeated lookups for missing keys
			k.cache.SetNegative(key)
			return nil, nil
		}

		config, err := datasource.ParseRouteConfig([]byte(data))
		if err != nil {
			k.logger.Warn("failed to parse route config",
				zap.String("key", key),
				zap.Error(err))
			return nil, nil
		}

		// Cache the result
		k.cache.Set(key, config)
		return config, nil
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.(*datasource.RouteConfig), nil
}

// Healthy returns the health status of the Kubernetes connection.
func (k *KubernetesSource) Healthy() bool {
	return k.healthy.Load()
}

// initialLoad loads all existing configurations from ConfigMap.
func (k *KubernetesSource) initialLoad() error {
	if k.LabelSelector != "" {
		return k.loadByLabelSelector()
	}
	return k.loadByConfigMapName()
}

// loadByConfigMapName loads a specific ConfigMap.
func (k *KubernetesSource) loadByConfigMapName() error {
	cm, err := k.client.CoreV1().ConfigMaps(k.Namespace).Get(k.ctx, k.ConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get configmap %s: %w", k.ConfigMapName, err)
	}

	return k.loadConfigMapData(cm)
}

// loadByLabelSelector loads ConfigMaps matching the label selector.
func (k *KubernetesSource) loadByLabelSelector() error {
	cmList, err := k.client.CoreV1().ConfigMaps(k.Namespace).List(k.ctx, metav1.ListOptions{
		LabelSelector: k.LabelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list configmaps: %w", err)
	}

	for i := range cmList.Items {
		if err := k.loadConfigMapData(&cmList.Items[i]); err != nil {
			k.logger.Warn("failed to load configmap",
				zap.String("name", cmList.Items[i].Name),
				zap.Error(err))
		}
	}

	return nil
}

// loadConfigMapData loads routing configurations from a ConfigMap.
func (k *KubernetesSource) loadConfigMapData(cm *corev1.ConfigMap) error {
	count := 0
	for key, data := range cm.Data {
		config, err := datasource.ParseRouteConfig([]byte(data))
		if err != nil {
			k.logger.Warn("failed to parse config",
				zap.String("configmap", cm.Name),
				zap.String("key", key),
				zap.Error(err))
			continue
		}

		k.cache.Set(key, config)
		count++
	}

	k.logger.Info("loaded configurations from configmap",
		zap.String("name", cm.Name),
		zap.Int("count", count))
	return nil
}

// watchConfigMaps watches for changes in ConfigMaps.
func (k *KubernetesSource) watchConfigMaps() {
	defer k.watcherWg.Done()

	for {
		select {
		case <-k.ctx.Done():
			return
		default:
		}

		var listOptions metav1.ListOptions
		if k.LabelSelector != "" {
			listOptions.LabelSelector = k.LabelSelector
		} else {
			listOptions.FieldSelector = fmt.Sprintf("metadata.name=%s", k.ConfigMapName)
		}

		watcher, err := k.client.CoreV1().ConfigMaps(k.Namespace).Watch(k.ctx, listOptions)
		if err != nil {
			k.logger.Error("failed to create configmap watcher", zap.Error(err))
			k.healthy.Store(false)
			time.Sleep(5 * time.Second)
			continue
		}

		k.healthy.Store(true)
		k.handleWatchEvents(watcher)
	}
}

// handleWatchEvents handles watch events from ConfigMaps.
func (k *KubernetesSource) handleWatchEvents(watcher watch.Interface) {
	defer watcher.Stop()

	for {
		select {
		case <-k.ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				k.logger.Debug("configmap watch channel closed, reconnecting")
				return
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				cm, ok := event.Object.(*corev1.ConfigMap)
				if !ok {
					continue
				}
				k.logger.Debug("configmap updated",
					zap.String("name", cm.Name),
					zap.String("type", string(event.Type)))
				k.handleConfigMapUpdate(cm)

			case watch.Deleted:
				cm, ok := event.Object.(*corev1.ConfigMap)
				if !ok {
					continue
				}
				k.logger.Debug("configmap deleted", zap.String("name", cm.Name))
				k.handleConfigMapDelete(cm)

			case watch.Error:
				k.logger.Warn("watch error", zap.Any("object", event.Object))
				k.healthy.Store(false)
				return
			}
		}
	}
}

// handleConfigMapUpdate handles ConfigMap add/update events.
func (k *KubernetesSource) handleConfigMapUpdate(cm *corev1.ConfigMap) {
	// Track existing keys from this configmap
	existingKeys := make(map[string]bool)

	for key, data := range cm.Data {
		config, err := datasource.ParseRouteConfig([]byte(data))
		if err != nil {
			k.logger.Warn("failed to parse config",
				zap.String("configmap", cm.Name),
				zap.String("key", key),
				zap.Error(err))
			continue
		}

		k.cache.Set(key, config)
		existingKeys[key] = true
	}

	// Remove keys that no longer exist (only if watching single configmap)
	if k.LabelSelector == "" {
		for _, key := range k.cache.Keys() {
			if !existingKeys[key] {
				k.cache.Delete(key)
			}
		}
	}
}

// handleConfigMapDelete handles ConfigMap delete events.
func (k *KubernetesSource) handleConfigMapDelete(cm *corev1.ConfigMap) {
	// Clear cache entries from this configmap
	for key := range cm.Data {
		k.cache.Delete(key)
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (k *KubernetesSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "namespace":
				if !d.NextArg() {
					return d.ArgErr()
				}
				k.Namespace = d.Val()
			case "configmap_name", "configmap":
				if !d.NextArg() {
					return d.ArgErr()
				}
				k.ConfigMapName = d.Val()
			case "label_selector":
				if !d.NextArg() {
					return d.ArgErr()
				}
				k.LabelSelector = d.Val()
			case "kubeconfig":
				if !d.NextArg() {
					return d.ArgErr()
				}
				k.Kubeconfig = d.Val()
			case "max_cache_size":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var size int
				if _, err := fmt.Sscanf(d.Val(), "%d", &size); err != nil {
					return d.Errf("invalid max_cache_size: %v", err)
				}
				k.MaxCacheSize = size
			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// Validate implements caddy.Validator.
func (k *KubernetesSource) Validate() error {
	if k.MaxCacheSize < 0 {
		return fmt.Errorf("max_cache_size must be non-negative")
	}
	return nil
}

// GetConfigMapExample returns an example ConfigMap for documentation.
func GetConfigMapExample() string {
	example := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "caddy-routing",
			"namespace": "default",
			"labels": map[string]string{
				"app": "caddy-dynamic-routing",
			},
		},
		"data": map[string]string{
			"tenant-a": `{"upstream": "backend-a:8080"}`,
			"tenant-b": `{"rules": [{"match": {"path": "/api/*"}, "upstreams": [{"address": "api-backend:8080"}]}], "fallback": "web-backend:8080"}`,
		},
	}

	data, _ := json.MarshalIndent(example, "", "  ")
	return string(data)
}

// Interface guards
var (
	_ caddy.Module          = (*KubernetesSource)(nil)
	_ caddy.Provisioner     = (*KubernetesSource)(nil)
	_ caddy.CleanerUpper    = (*KubernetesSource)(nil)
	_ caddy.Validator       = (*KubernetesSource)(nil)
	_ datasource.DataSource = (*KubernetesSource)(nil)
	_ caddyfile.Unmarshaler = (*KubernetesSource)(nil)
)
