.PHONY: help build test lint clean run docker-build docker-run

# Variables
VERSION := $(shell cat VERSION)
BINARY_NAME := esi-proxy
DOCKER_IMAGE := ghcr.io/sternrassler/eve-esi-client
GO := go
GOFLAGS := -v

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the ESI proxy binary
	@echo "Building $(BINARY_NAME) v$(VERSION)..."
	$(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/esi-proxy

test: ## Run tests
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run ./...

fmt: ## Format code
	@echo "Formatting code..."
	$(GO) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	$(GO) vet ./...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -f coverage.out coverage.html

run: ## Run the ESI proxy service
	@echo "Starting ESI proxy on :8080..."
	$(GO) run ./cmd/esi-proxy

docker-build: ## Build Docker image
	@echo "Building Docker image $(DOCKER_IMAGE):$(VERSION)..."
	docker build -t $(DOCKER_IMAGE):$(VERSION) -t $(DOCKER_IMAGE):latest .

docker-run: ## Run Docker container
	docker run --rm -p 8080:8080 \
		-e REDIS_URL=host.docker.internal:6379 \
		$(DOCKER_IMAGE):latest

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download

tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	$(GO) mod tidy

.DEFAULT_GOAL := help
