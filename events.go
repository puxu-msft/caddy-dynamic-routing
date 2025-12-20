// Package caddyslb provides events for the dynamic load balancer.
package caddyslb

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

// Event names for the dynamic load balancer.
const (
	// EventRouteSelected is emitted when a route is successfully selected.
	EventRouteSelected = "dynamic_lb.route_selected"

	// EventRouteMissed is emitted when no route could be selected (fallback used).
	EventRouteMissed = "dynamic_lb.route_missed"

	// EventConfigUpdated is emitted when a route configuration is updated.
	EventConfigUpdated = "dynamic_lb.config_updated"

	// EventConfigDeleted is emitted when a route configuration is deleted.
	EventConfigDeleted = "dynamic_lb.config_deleted"

	// EventDataSourceHealthChanged is emitted when a data source health status changes.
	EventDataSourceHealthChanged = "dynamic_lb.datasource_health_changed"
)

// RouteSelectedEvent contains data for the route_selected event.
type RouteSelectedEvent struct {
	// Key is the routing key that was used.
	Key string `json:"key"`

	// Upstream is the selected upstream address.
	Upstream string `json:"upstream"`

	// Algorithm is the load balancing algorithm used.
	Algorithm string `json:"algorithm,omitempty"`

	// Duration is the time taken to select the route.
	Duration time.Duration `json:"duration_ns"`

	// ConfigVersion is the version of the route config used.
	ConfigVersion int64 `json:"config_version,omitempty"`

	// RequestPath is the request URI path.
	RequestPath string `json:"request_path"`

	// RequestMethod is the HTTP method.
	RequestMethod string `json:"request_method"`
}

// RouteMissedEvent contains data for the route_missed event.
type RouteMissedEvent struct {
	// Key is the routing key that was attempted.
	Key string `json:"key,omitempty"`

	// Reason is why the route was missed.
	Reason string `json:"reason"`

	// FallbackUsed is the fallback policy that was used.
	FallbackUsed string `json:"fallback_used,omitempty"`

	// RequestPath is the request URI path.
	RequestPath string `json:"request_path"`

	// RequestMethod is the HTTP method.
	RequestMethod string `json:"request_method"`
}

// ConfigUpdatedEvent contains data for the config_updated event.
type ConfigUpdatedEvent struct {
	// Key is the routing key that was updated.
	Key string `json:"key"`

	// SourceType is the data source type.
	SourceType string `json:"source_type"`

	// OldVersion is the previous version number.
	OldVersion int64 `json:"old_version,omitempty"`

	// NewVersion is the new version number.
	NewVersion int64 `json:"new_version,omitempty"`

	// Timestamp is when the update occurred.
	Timestamp time.Time `json:"timestamp"`
}

// ConfigDeletedEvent contains data for the config_deleted event.
type ConfigDeletedEvent struct {
	// Key is the routing key that was deleted.
	Key string `json:"key"`

	// SourceType is the data source type.
	SourceType string `json:"source_type"`

	// LastVersion is the version before deletion.
	LastVersion int64 `json:"last_version,omitempty"`

	// Timestamp is when the deletion occurred.
	Timestamp time.Time `json:"timestamp"`
}

// DataSourceHealthChangedEvent contains data for the datasource_health_changed event.
type DataSourceHealthChangedEvent struct {
	// SourceType is the data source type.
	SourceType string `json:"source_type"`

	// Healthy indicates the new health status.
	Healthy bool `json:"healthy"`

	// PreviousHealthy indicates the previous health status.
	PreviousHealthy bool `json:"previous_healthy"`

	// Error is the error message if unhealthy.
	Error string `json:"error,omitempty"`

	// Timestamp is when the status changed.
	Timestamp time.Time `json:"timestamp"`
}

// EventEmitter provides methods to emit dynamic load balancer events.
type EventEmitter struct {
	enabled bool
}

// NewEventEmitter creates a new EventEmitter.
func NewEventEmitter(enabled bool) *EventEmitter {
	return &EventEmitter{enabled: enabled}
}

// EmitRouteSelected emits a route_selected event.
func (e *EventEmitter) EmitRouteSelected(ctx context.Context, key, upstream, algorithm string, duration time.Duration, configVersion int64, r *http.Request) {
	if !e.enabled {
		return
	}

	event := RouteSelectedEvent{
		Key:           key,
		Upstream:      upstream,
		Algorithm:     algorithm,
		Duration:      duration,
		ConfigVersion: configVersion,
		RequestPath:   r.URL.Path,
		RequestMethod: r.Method,
	}

	caddy.Log().Named("events").Debug("emitting route_selected event",
		zap.String("key", key),
		zap.String("upstream", upstream),
	)

	// Publish to internal event bus
	globalEventBus.Publish(EventRouteSelected, event)
}

// EmitRouteMissed emits a route_missed event.
func (e *EventEmitter) EmitRouteMissed(ctx context.Context, key, reason, fallbackUsed string, r *http.Request) {
	if !e.enabled {
		return
	}

	event := RouteMissedEvent{
		Key:           key,
		Reason:        reason,
		FallbackUsed:  fallbackUsed,
		RequestPath:   r.URL.Path,
		RequestMethod: r.Method,
	}

	caddy.Log().Named("events").Debug("emitting route_missed event",
		zap.String("key", key),
		zap.String("reason", reason),
	)

	globalEventBus.Publish(EventRouteMissed, event)
}

// EmitConfigUpdated emits a config_updated event.
func (e *EventEmitter) EmitConfigUpdated(sourceType, key string, oldVersion, newVersion int64) {
	if !e.enabled {
		return
	}

	event := ConfigUpdatedEvent{
		Key:        key,
		SourceType: sourceType,
		OldVersion: oldVersion,
		NewVersion: newVersion,
		Timestamp:  time.Now(),
	}

	caddy.Log().Named("events").Debug("emitting config_updated event",
		zap.String("key", key),
		zap.String("source_type", sourceType),
	)

	globalEventBus.Publish(EventConfigUpdated, event)
}

// EmitConfigDeleted emits a config_deleted event.
func (e *EventEmitter) EmitConfigDeleted(sourceType, key string, lastVersion int64) {
	if !e.enabled {
		return
	}

	event := ConfigDeletedEvent{
		Key:         key,
		SourceType:  sourceType,
		LastVersion: lastVersion,
		Timestamp:   time.Now(),
	}

	caddy.Log().Named("events").Debug("emitting config_deleted event",
		zap.String("key", key),
		zap.String("source_type", sourceType),
	)

	globalEventBus.Publish(EventConfigDeleted, event)
}

// EmitDataSourceHealthChanged emits a datasource_health_changed event.
func (e *EventEmitter) EmitDataSourceHealthChanged(sourceType string, healthy, previousHealthy bool, errorMsg string) {
	if !e.enabled {
		return
	}

	event := DataSourceHealthChangedEvent{
		SourceType:      sourceType,
		Healthy:         healthy,
		PreviousHealthy: previousHealthy,
		Error:           errorMsg,
		Timestamp:       time.Now(),
	}

	caddy.Log().Named("events").Debug("emitting datasource_health_changed event",
		zap.String("source_type", sourceType),
		zap.Bool("healthy", healthy),
	)

	globalEventBus.Publish(EventDataSourceHealthChanged, event)
}

// EventHandler is a callback function for handling events.
type EventHandler func(eventName string, data interface{})

// EventBus is a simple in-process event bus for components to subscribe to events.
// It is safe for concurrent use.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[string][]EventHandler),
	}
}

// Subscribe registers a handler for an event.
// This method is safe for concurrent use.
func (eb *EventBus) Subscribe(eventName string, handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.handlers[eventName] = append(eb.handlers[eventName], handler)
}

// Publish sends an event to all registered handlers.
// Handlers are called synchronously in the order they were registered.
// This method is safe for concurrent use.
func (eb *EventBus) Publish(eventName string, data interface{}) {
	eb.mu.RLock()
	// Copy handlers to avoid holding lock during handler execution
	handlers := make([]EventHandler, 0, len(eb.handlers[eventName])+len(eb.handlers["*"]))
	handlers = append(handlers, eb.handlers[eventName]...)
	handlers = append(handlers, eb.handlers["*"]...)
	eb.mu.RUnlock()

	for _, handler := range handlers {
		handler(eventName, data)
	}
}

// Global event bus for internal use
var globalEventBus = NewEventBus()

// GetEventBus returns the global event bus.
func GetEventBus() *EventBus {
	return globalEventBus
}
