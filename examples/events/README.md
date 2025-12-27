# Event System Examples

Subscribe to routing events for monitoring, logging, and custom integrations.

Navigation:

- Back to examples index: [../README.md](../README.md)
- Related: [../admin-api/README.md](../admin-api/README.md)
- Related: [../metrics/README.md](../metrics/README.md)

Note: the core routing path does not emit these events by default. Events are emitted only when code explicitly creates and uses an `EventEmitter` (see the `caddyslb` package). Treat this directory as an API/how-to reference for wiring events into your own integration.

## Available Events

| Event | Description |
|-------|-------------|
| `dynamic_lb.route_selected` | Route successfully selected |
| `dynamic_lb.route_missed` | No route found, fallback used |
| `dynamic_lb.config_updated` | Route configuration updated |
| `dynamic_lb.config_deleted` | Route configuration deleted |
| `dynamic_lb.datasource_health_changed` | Data source health changed |

## Basic Usage

```go
package main

import (
    "fmt"
    "log"

    caddyslb "github.com/puxu-msft/caddy-dynamic-routing"
)

func main() {
    bus := caddyslb.GetEventBus()

    // Subscribe to route selections
    bus.Subscribe(caddyslb.EventRouteSelected, func(name string, data interface{}) {
        event := data.(caddyslb.RouteSelectedEvent)
        log.Printf("Route selected: key=%s upstream=%s duration=%v",
            event.Key, event.Upstream, event.Duration)
    })

    // Subscribe to route misses
    bus.Subscribe(caddyslb.EventRouteMissed, func(name string, data interface{}) {
        event := data.(caddyslb.RouteMissedEvent)
        log.Printf("Route missed: key=%s reason=%s fallback=%s",
            event.Key, event.Reason, event.FallbackUsed)
    })
}
```

## Metrics Collection

```go
func setupMetricsCollection() {
    bus := caddyslb.GetEventBus()

    // Track route hit rate per tenant
    hitCounts := make(map[string]int64)
    missCounts := make(map[string]int64)

    bus.Subscribe(caddyslb.EventRouteSelected, func(_ string, data interface{}) {
        event := data.(caddyslb.RouteSelectedEvent)
        hitCounts[event.Key]++
    })

    bus.Subscribe(caddyslb.EventRouteMissed, func(_ string, data interface{}) {
        event := data.(caddyslb.RouteMissedEvent)
        missCounts[event.Key]++
    })
}
```

## Audit Logging

```go
func setupAuditLogging() {
    bus := caddyslb.GetEventBus()

    // Log all configuration changes
    bus.Subscribe(caddyslb.EventConfigUpdated, func(_ string, data interface{}) {
        event := data.(caddyslb.ConfigUpdatedEvent)
        auditLog.Info("config updated",
            "key", event.Key,
            "source", event.SourceType,
            "old_version", event.OldVersion,
            "new_version", event.NewVersion,
            "timestamp", event.Timestamp,
        )
    })

    bus.Subscribe(caddyslb.EventConfigDeleted, func(_ string, data interface{}) {
        event := data.(caddyslb.ConfigDeletedEvent)
        auditLog.Info("config deleted",
            "key", event.Key,
            "source", event.SourceType,
            "last_version", event.LastVersion,
            "timestamp", event.Timestamp,
        )
    })
}
```

## Health Monitoring

```go
func setupHealthMonitoring() {
    bus := caddyslb.GetEventBus()

    bus.Subscribe(caddyslb.EventDataSourceHealthChanged, func(_ string, data interface{}) {
        event := data.(caddyslb.DataSourceHealthChangedEvent)

        if !event.Healthy {
            alerting.Send(alerting.Critical,
                fmt.Sprintf("Data source %s became unhealthy: %s",
                    event.SourceType, event.Error))
        } else if event.PreviousHealthy == false {
            alerting.Send(alerting.Info,
                fmt.Sprintf("Data source %s recovered", event.SourceType))
        }
    })
}
```

## Wildcard Subscription

```go
// Subscribe to all events
bus.Subscribe("*", func(eventName string, data interface{}) {
    log.Printf("[EVENT] %s: %+v", eventName, data)
})
```

## Event Data Structures

### RouteSelectedEvent

```go
type RouteSelectedEvent struct {
    Key           string        // Routing key
    Upstream      string        // Selected upstream
    Algorithm     string        // Algorithm used
    Duration      time.Duration // Selection time
    ConfigVersion int64         // Config version
    RequestPath   string        // Request URI
    RequestMethod string        // HTTP method
}
```

### RouteMissedEvent

```go
type RouteMissedEvent struct {
    Key           string // Routing key attempted
    Reason        string // Why it missed
    FallbackUsed  string // Fallback policy name
    RequestPath   string // Request URI
    RequestMethod string // HTTP method
}
```

### ConfigUpdatedEvent

```go
type ConfigUpdatedEvent struct {
    Key        string    // Routing key
    SourceType string    // Data source type
    OldVersion int64     // Previous version
    NewVersion int64     // New version
    Timestamp  time.Time // When updated
}
```

### DataSourceHealthChangedEvent

```go
type DataSourceHealthChangedEvent struct {
    SourceType      string    // Data source type
    Healthy         bool      // Current health
    PreviousHealthy bool      // Previous health
    Error           string    // Error message (if unhealthy)
    Timestamp       time.Time // When changed
}
```
