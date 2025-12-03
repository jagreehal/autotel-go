# Contributing to autotel-go

Thank you for your interest in contributing to autotel-go! This document provides guidelines and instructions for contributing.

## Development Setup

1. **Fork and clone the repository:**
   ```bash
   git clone https://github.com/yourusername/autotel-go.git
   cd autotel-go
   ```

2. **Install dependencies:**
   ```bash
   go mod download
   ```

3. **Run tests:**
   ```bash
   make test
   # or
   go test ./...
   ```

4. **Run linters:**
   ```bash
   make lint
   # or
   golangci-lint run
   ```

## Code Style

- Follow standard Go formatting (`gofmt`, `goimports`)
- Follow Go naming conventions
- Write comprehensive tests for new features
- Add documentation comments for exported functions
- Keep functions focused and small

## Testing

- Write unit tests for all new features
- Aim for >80% test coverage
- Use table-driven tests where appropriate
- Test error cases and edge cases

## Pull Request Process

1. Create a feature branch from `main`
2. Make your changes with tests
3. Ensure all tests pass: `go test ./...`
4. Ensure code is formatted: `make format`
5. Ensure linters pass: `make lint`
6. Update documentation if needed
7. Submit a pull request with a clear description

## Project Structure

```
autotel-go/
├── Core tracing API (autotel.go, span.go, tracer.go)
├── Production features (sampling/, ratelimit/, circuitbreaker/, redaction/)
├── Framework integrations (middleware/)
├── Advanced features (logging/, analytics/)
├── Testing utilities (testing/)
└── Examples (examples/)
```

## Adding New Features

- **Analytics Adapters**: Add to `analytics/adapters/`
- **Middleware**: Add to `middleware/`
- **Production Features**: Add to appropriate subdirectory
- **Examples**: Add to `examples/`

## Questions?

Open an issue for discussion before starting work on major features.
