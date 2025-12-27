# caddy-dynamic-routing Makefile

.PHONY: all build test lint clean coverage vet fmt help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOFMT=gofmt
GOMOD=$(GOCMD) mod

# Build parameters
BINARY_NAME=caddy
BUILD_DIR=build

# Default target
all: lint test build

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## build: Build caddy with the plugin
build:
	@echo "Building caddy with caddy-dynamic-routing plugin..."
	xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing=./
	@echo "Done! Binary: ./caddy"

## test: Run all tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-race: Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	$(GOTEST) -race -v ./...

## coverage: Run tests with coverage
coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## coverage-report: Show coverage summary
coverage-report: coverage
	@echo ""
	@echo "Coverage summary:"
	@$(GOCMD) tool cover -func=coverage.out | grep total

## lint: Run golangci-lint
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	elif [ -x "$$($(GOCMD) env GOPATH)/bin/golangci-lint" ]; then \
		"$$($(GOCMD) env GOPATH)/bin/golangci-lint" run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2"; \
	fi

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

## fmt-check: Check code formatting
fmt-check:
	@echo "Checking code formatting..."
	@if [ -n "$$($(GOFMT) -l .)" ]; then \
		echo "Code is not formatted. Run 'make fmt' to fix."; \
		$(GOFMT) -l .; \
		exit 1; \
	fi

## tidy: Tidy go modules
tidy:
	@echo "Tidying go modules..."
	$(GOMOD) tidy

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

## verify: Verify dependencies
verify:
	@echo "Verifying dependencies..."
	$(GOMOD) verify

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f caddy
	rm -f coverage.out coverage.html
	rm -rf $(BUILD_DIR)

## docker-up: Start docker compose services for testing
docker-up:
	@echo "Starting docker services..."
	docker compose -f examples/docker/docker-compose.yml up --build -d

## docker-down: Stop docker compose services
docker-down:
	@echo "Stopping docker services..."
	docker compose -f examples/docker/docker-compose.yml down -v

## check: Run all checks (fmt, vet, lint, test)
check: fmt-check vet lint test
	@echo "All checks passed!"
