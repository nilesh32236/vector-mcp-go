.PHONY: all build test clean run version help fmt lint

BINARY_NAME=vector-mcp-go
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.Commit=$(COMMIT)"

all: fmt lint test build

fmt:
	gofmt -s -w .

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
	@echo "make all     - Format, lint, test, and build the binary (Standard Workflow)"
	@echo "make fmt     - Format and simplify the codebase using gofmt -s"
	@echo "make lint    - Run static analysis using go vet"
	@echo "make build   - Compile the binary"
	@echo "make test    - Execute all unit tests"
	@echo "make clean   - Cleanup build artifacts and dist folder"
	@echo "make run     - Build and execute the binary locally"
	@echo "make version - Display version, build time, and commit hash"
