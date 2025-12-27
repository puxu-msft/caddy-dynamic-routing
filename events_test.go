package caddyslb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventEmitter_Disabled(t *testing.T) {
	emitter := NewEventEmitter(false)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	// Should not panic when disabled
	emitter.EmitRouteSelected(context.Background(), "key", "upstream", "algo", time.Second, 1, req)
	emitter.EmitRouteMissed(context.Background(), "key", "reason", "fallback", req)
	emitter.EmitConfigUpdated("etcd", "key", 1, 2)
	emitter.EmitConfigDeleted("etcd", "key", 1)
	emitter.EmitDataSourceHealthChanged("etcd", true, false, "")
}

func TestEventEmitter_Enabled(t *testing.T) {
	emitter := NewEventEmitter(true)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	// Should not panic when enabled
	emitter.EmitRouteSelected(context.Background(), "key", "upstream", "algo", time.Second, 1, req)
	emitter.EmitRouteMissed(context.Background(), "key", "reason", "fallback", req)
	emitter.EmitConfigUpdated("etcd", "key", 1, 2)
	emitter.EmitConfigDeleted("etcd", "key", 1)
	emitter.EmitDataSourceHealthChanged("etcd", true, false, "connection error")
}

func TestEventBus_Subscribe_Publish(t *testing.T) {
	bus := NewEventBus()

	received := make(chan interface{}, 1)
	bus.Subscribe(EventRouteSelected, func(eventName string, data interface{}) {
		received <- data
	})

	testEvent := RouteSelectedEvent{
		Key:      "test-key",
		Upstream: "backend:8080",
	}
	bus.Publish(EventRouteSelected, testEvent)

	select {
	case data := <-received:
		event, ok := data.(RouteSelectedEvent)
		if !ok {
			t.Fatal("Expected RouteSelectedEvent")
		}
		if event.Key != "test-key" {
			t.Errorf("Expected key 'test-key', got '%s'", event.Key)
		}
	case <-time.After(time.Second):
		t.Fatal("Event not received")
	}
}

func TestEventBus_WildcardSubscriber(t *testing.T) {
	bus := NewEventBus()

	count := 0
	bus.Subscribe("*", func(eventName string, data interface{}) {
		count++
	})

	bus.Publish(EventRouteSelected, nil)
	bus.Publish(EventRouteMissed, nil)
	bus.Publish(EventConfigUpdated, nil)

	if count != 3 {
		t.Errorf("Expected 3 events, got %d", count)
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()

	count := 0
	bus.Subscribe(EventRouteSelected, func(eventName string, data interface{}) {
		count++
	})
	bus.Subscribe(EventRouteSelected, func(eventName string, data interface{}) {
		count++
	})

	bus.Publish(EventRouteSelected, nil)

	if count != 2 {
		t.Errorf("Expected 2 handler calls, got %d", count)
	}
}

func TestGetEventBus(t *testing.T) {
	bus := GetEventBus()
	if bus == nil {
		t.Fatal("GetEventBus returned nil")
	}

	// Should return the same instance
	bus2 := GetEventBus()
	if bus != bus2 {
		t.Error("GetEventBus should return the same instance")
	}
}

func TestRouteSelectedEvent_Fields(t *testing.T) {
	event := RouteSelectedEvent{
		Key:           "test-key",
		Upstream:      "backend:8080",
		Algorithm:     "round_robin",
		Duration:      100 * time.Millisecond,
		ConfigVersion: 5,
		RequestPath:   "/api/v1/users",
		RequestMethod: "GET",
	}

	if event.Key != "test-key" {
		t.Errorf("Key mismatch")
	}
	if event.Upstream != "backend:8080" {
		t.Errorf("Upstream mismatch")
	}
	if event.Algorithm != "round_robin" {
		t.Errorf("Algorithm mismatch")
	}
	if event.Duration != 100*time.Millisecond {
		t.Errorf("Duration mismatch")
	}
	if event.ConfigVersion != 5 {
		t.Errorf("ConfigVersion mismatch")
	}
	if event.RequestPath != "/api/v1/users" {
		t.Errorf("RequestPath mismatch")
	}
	if event.RequestMethod != "GET" {
		t.Errorf("RequestMethod mismatch")
	}
}

func TestRouteMissedEvent_Fields(t *testing.T) {
	event := RouteMissedEvent{
		Key:           "test-key",
		Reason:        "no_config",
		FallbackUsed:  "random",
		RequestPath:   "/api/v1/users",
		RequestMethod: "POST",
	}

	if event.Key != "test-key" {
		t.Errorf("Key mismatch")
	}
	if event.Reason != "no_config" {
		t.Errorf("Reason mismatch")
	}
	if event.FallbackUsed != "random" {
		t.Errorf("FallbackUsed mismatch")
	}
	if event.RequestPath != "/api/v1/users" {
		t.Errorf("RequestPath mismatch")
	}
	if event.RequestMethod != "POST" {
		t.Errorf("RequestMethod mismatch")
	}
}

func TestConfigUpdatedEvent_Fields(t *testing.T) {
	now := time.Now()
	event := ConfigUpdatedEvent{
		Key:        "tenant-a",
		SourceType: "etcd",
		OldVersion: 1,
		NewVersion: 2,
		Timestamp:  now,
	}

	if event.Key != "tenant-a" {
		t.Errorf("Key mismatch")
	}
	if event.SourceType != "etcd" {
		t.Errorf("SourceType mismatch")
	}
	if event.OldVersion != 1 || event.NewVersion != 2 {
		t.Errorf("Version mismatch")
	}
	if !event.Timestamp.Equal(now) {
		t.Errorf("Timestamp mismatch")
	}
}

func TestDataSourceHealthChangedEvent_Fields(t *testing.T) {
	now := time.Now()
	event := DataSourceHealthChangedEvent{
		SourceType:      "redis",
		Healthy:         false,
		PreviousHealthy: true,
		Error:           "connection refused",
		Timestamp:       now,
	}

	if event.SourceType != "redis" {
		t.Errorf("SourceType mismatch")
	}
	if event.Healthy != false || event.PreviousHealthy != true {
		t.Errorf("Health status mismatch")
	}
	if event.Error != "connection refused" {
		t.Errorf("Error mismatch")
	}
	if !event.Timestamp.Equal(now) {
		t.Errorf("Timestamp mismatch")
	}
}

func TestEventBus_Concurrent(t *testing.T) {
	bus := NewEventBus()

	var count atomic.Int64
	const numSubscribers = 5
	const numPublishers = 10
	const numPublishes = 100

	// Add multiple subscribers
	for i := 0; i < numSubscribers; i++ {
		bus.Subscribe(EventRouteSelected, func(eventName string, data interface{}) {
			count.Add(1)
		})
	}

	var wg sync.WaitGroup

	// Concurrent publishers
	for i := 0; i < numPublishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numPublishes; j++ {
				bus.Publish(EventRouteSelected, RouteSelectedEvent{
					Key:      "test-key",
					Upstream: "backend:8080",
				})
			}
		}()
	}

	wg.Wait()

	// Each publish should trigger all subscribers
	expected := int64(numSubscribers * numPublishers * numPublishes)
	if count.Load() != expected {
		t.Errorf("Expected %d handler calls, got %d", expected, count.Load())
	}
}

func TestEventBus_ConcurrentSubscribePublish(t *testing.T) {
	bus := NewEventBus()

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOperations = 50

	// Concurrent subscribe and publish
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Subscriber goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				bus.Subscribe(EventConfigUpdated, func(eventName string, data interface{}) {
					// Do nothing, just testing concurrency safety
				})
			}
		}()

		// Publisher goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				bus.Publish(EventConfigUpdated, ConfigUpdatedEvent{
					Key:        "test",
					SourceType: "etcd",
				})
			}
		}()
	}

	wg.Wait()
}
