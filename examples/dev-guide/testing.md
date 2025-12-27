# Testing Guidelines

This document contains testing patterns and illustrative code examples referenced from root docs.

## Table-driven tests (example)

```go
func TestParseRouteConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "invalid", input: `{invalid}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRouteConfig([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
```

## Concurrency tests (example)

```go
func TestUpstreamSelector_Concurrent(t *testing.T) {
	selector := NewUpstreamSelector()
	upstreams := []Upstream{{Address: "backend1:8080", Weight: 1}}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = selector.Select(upstreams, nil, "round_robin", "")
			}
		}()
	}
	wg.Wait()
}
```

## Mocking

Prefer interfaces + dependency injection so unit tests don’t require real external services.

## Running tests (commands)

```bash
# All tests
go test ./...

# With race detection (recommended for changes touching concurrency)
go test -race ./...

# Go vet
go vet ./...

# Package-only
go test ./matcher/...

# Single test
go test -run TestUpstreamSelector_Concurrent ./matcher/...

# Coverage (profile)
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```
