.PHONY: all build test clean run version help fmt lint

BINARY_NAME=vector-mcp-go
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.Commit=$(COMMIT)"

all: fmt lint test build

fmt:
	go fmt ./...

lint:
	go vet ./...

build:
	go build $(LDFLAGS) -o $(BINARY_NAME) main.go

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	rm -rf dist/

run: build
	./$(BINARY_NAME)

version:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Commit: $(COMMIT)"

help:
	@echo "make all     - Format, lint, test, and build the binary"
	@echo "make fmt     - Format the codebase"
	@echo "make lint    - Run go vet on the codebase"
	@echo "make build   - Build the binary"
	@echo "make test    - Run tests"
	@echo "make clean   - Remove binary and dist folder"
	@echo "make run     - Build and run the binary"
	@echo "make version - Show version information"
