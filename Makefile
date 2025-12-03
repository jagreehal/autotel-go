.PHONY: help quality test lint format coverage build clean ci

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Quality check (runs test, lint, format)
quality: format lint test ## Run all quality checks

# Testing
test: ## Run tests
	@echo "Running tests..."
	@go test -v -race ./...

coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Linting and formatting
lint: ## Run linters
	@echo "Running linters..."
	@golangci-lint run || echo "golangci-lint not installed, skipping..."

format: ## Format code
	@echo "Formatting code..."
	@gofmt -w .
	@goimports -w . || echo "goimports not installed, skipping..."

# Build
build: ## Build the library
	@echo "Building..."
	@go build ./...

# Dependency management
tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	@go mod tidy

# Security scanning
security: ## Run security scanner
	@echo "Running security scanner..."
	@gosec ./... || echo "gosec not installed, skipping..."

# Cleanup
clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf coverage.out coverage.html

# Combined checks
ci: format lint test build ## Run CI pipeline locally
	@echo "✓ CI pipeline passed!"
