# autotel-go

<div align="center">

[![Go Reference](https://pkg.go.dev/badge/github.com/jagreehal/autotel-go.svg)](https://pkg.go.dev/github.com/jagreehal/autotel-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/jagreehal/autotel-go)](https://goreportcard.com/report/github.com/jagreehal/autotel-go)

</div>

OpenTelemetry instrumentation for Go.

- One-line initialization with `Init()` and `Start()` helpers
- OTLP-first design with subscribers for PostHog, Mixpanel, Amplitude, Webhook, and custom destinations
- Production features: adaptive sampling, rate limiting, circuit breakers, PII redaction
- Automatic enrichment: trace context flows into spans, logs, and events

OpenTelemetry requires significant boilerplate. Autotel provides a simpler API while maintaining full control over your telemetry.

```bash
go get github.com/jagreehal/autotel-go
```

## Quick Start

### 1. Initialize once at startup

```go
import "github.com/jagreehal/autotel-go"

func main() {
    cleanup, err := autotel.Init(context.Background(),
        autotel.WithService("my-service"),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer cleanup()
}
```

**Configuration options:**

- Environment variables: `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_HEADERS`, `AUTOTEL_DEBUG`, etc.
- Explicit parameters override env vars
- No OTLP exporters run unless you set `WithEndpoint(...)` or the OTEL env vars—making debug-only or subscriber-only setups zero-config.
- Vendor presets (OTLP-first): `WithBackend("datadog"|"honeycomb"|"grafana")` sets endpoint + headers; use `WithOTLPHeaders` for API keys/datasets without extra SDKs.
- Metrics on by default: OTLP metric exporter is wired alongside traces. Toggle with `WithMetrics(false)` or customize with `WithMetricExporters`/`WithMetricInterval`.

#### Environment variables (optional)

You can configure autotel-go entirely via standard OTEL env vars—the SDK only falls back to functional options if the env var is missing:

- `OTEL_SERVICE_NAME` – overrides `WithService`.
- `OTEL_EXPORTER_OTLP_ENDPOINT` – host:port or URL; when set we enable OTLP trace + metric exporters.
- `OTEL_EXPORTER_OTLP_PROTOCOL` – `http` (default) or `grpc`.
- `OTEL_EXPORTER_OTLP_HEADERS` – comma-separated `key=value` pairs for API keys/datasets.
- `OTEL_RESOURCE_ATTRIBUTES` – comma-separated `key=value` attributes (e.g., `service.version=1.0.0,deployment.environment=prod`).
- `AUTOTEL_DEBUG` – `1/true` to mirror `WithDebug(true)` without code changes.

Environment values are sanitized (e.g., stripping `http://` prefixes) so you can copy URLs from docs without worrying about exporter formatting.

### 2. Instrument code with `Start()`

```go
import "github.com/jagreehal/autotel-go"

func CreateUser(ctx context.Context, data UserData) (*User, error) {
    ctx, span := autotel.Start(ctx, "CreateUser")
    defer span.End()

    span.SetAttribute("user.email", data.Email)

    user, err := db.Users.Create(ctx, data)
    if err != nil {
        span.RecordError(err)
        return nil, err
    }

    return user, nil
}
```

- Errors are recorded automatically with `Trace()` helper
- Works with any context-aware code

### 3. Track product events

```go
import (
    "github.com/jagreehal/autotel-go"
    "github.com/jagreehal/autotel-go/subscribers"
)

cleanup, err := autotel.Init(context.Background(),
    autotel.WithService("my-service"),
    autotel.WithSubscribers(
        subscribers.NewPostHogSubscriber("phc_..."),
    ),
)
defer cleanup()

func ProcessOrder(ctx context.Context, order Order) error {
    ctx, span := autotel.Start(ctx, "ProcessOrder")
    defer span.End()

    // Events automatically include trace_id and span_id
    autotel.Track(ctx, "order.completed", map[string]any{
        "amount": order.Total,
    })

    return charge(order)
}
```

Every span, log, and event includes `trace_id` and `span_id` automatically.

### 4. Capture metrics with trace correlation

```go
import "github.com/jagreehal/autotel-go"

m := autotel.Meter()
m.Counter(ctx, "checkout.requests", 1, map[string]any{"region": "iad"})
m.Histogram(ctx, "checkout.latency_ms", float64(duration.Milliseconds()), nil)
// trace_id/span_id are attached automatically when a span is present
```

Event delivery is hardened by default (buffer=1000, backoff 100ms→5s, circuit threshold=5, reset every 10s). Tune with `WithEventQueue`, `WithEventBackoff`, and `WithEventRetry`.

## Features

- ✅ **One-line initialization** - No boilerplate
- ✅ **Ergonomic API** - `Start()` and `Trace()` helpers
- ✅ **Production-ready** - Adaptive sampling, rate limiting, circuit breakers
- ✅ **PII redaction** - Built-in PII detection and redaction
- ✅ **Event tracking** - PostHog, Mixpanel, Amplitude, Webhook subscribers
- ✅ **Framework integrations** - HTTP (net/http), gRPC, Gin middleware
- ✅ **Vendor lock-in free** - Uses standard OpenTelemetry, works with any OTLP backend

## Comparison

| Feature             | Raw OpenTelemetry                 | autotel-go            |
| ------------------- | --------------------------------- | ------------------------- |
| Initialization      | 20-30 lines                       | 1 line (`Init()`)         |
| Span creation       | `tracer.Start()` + manual `End()` | `Start()` with defer      |
| Error recording     | Manual                            | Automatic with `Trace()`  |
| Adaptive sampling   | ❌ (collector only)               | ✅ Built-in               |
| Rate limiting       | ❌                                | ✅ Built-in               |
| PII redaction       | ❌                                | ✅ Built-in               |
| Product events      | ❌                                | ✅ Built-in (subscribers) |
| HTTP middleware     | Manual                            | `HTTPMiddleware()`        |
| Convenience helpers | Manual                            | ✅ Built-in               |

## Basic Usage

### Simple function tracing

```go
func GetUser(ctx context.Context, id string) (*User, error) {
    ctx, span := autotel.Start(ctx, "GetUser")
    defer span.End()

    span.SetAttribute("user.id", id)
    return db.Users.FindByID(ctx, id)
}
```

### With Trace() helper

```go
func CreateUser(ctx context.Context, data UserData) (*User, error) {
    return autotel.Trace(ctx, "CreateUser", func(ctx context.Context, span autotel.Span) (*User, error) {
        span.SetAttribute("user.email", data.Email)
        return db.Users.Create(ctx, data)
    })
}
```

### HTTP middleware (net/http)

```go
import (
    "net/http"
    "github.com/jagreehal/autotel-go/middleware"
)

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/users", handleUsers)

    handler := middleware.HTTPMiddleware("my-service")(mux)
    http.ListenAndServe(":8080", handler)
}
```

### Gin middleware

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/jagreehal/autotel-go/middleware"
)

func main() {
    r := gin.Default()
    r.Use(middleware.GinMiddleware("my-service"))
    r.GET("/users/:id", handleGetUser)
    r.Run(":8080")
}
```

### gRPC instrumentation

```go
import (
    "google.golang.org/grpc"
    "github.com/jagreehal/autotel-go/middleware"
)

// Server
server := grpc.NewServer(
    grpc.StatsHandler(middleware.GRPCServerHandler()),
)

// Client
conn, err := grpc.NewClient("localhost:50051",
    grpc.WithStatsHandler(middleware.GRPCClientHandler()),
)
```

## Advanced Features

### Structured Logging

Automatically inject trace context into logs using `log/slog`:

```go
import (
    "log/slog"
    "github.com/jagreehal/autotel-go/logging"
)

// Option 1: Automatic enrichment with TraceHandler
logger := slog.New(logging.NewTraceHandler(
    slog.NewJSONHandler(os.Stdout, nil),
))

ctx, span := autotel.Start(ctx, "operation")
defer span.End()
logger.InfoContext(ctx, "Processing request") // trace_id and span_id automatically added

// Option 2: Manual enrichment
attrs := logging.WithTraceContext(ctx)
logger.InfoContext(ctx, "Processing request", slog.Group("trace", attrs...))
```

### Event Tracking (Product Events)

Track product events with automatic trace context enrichment. Events are sent to subscribers (PostHog, Mixpanel, Amplitude, Webhook, etc.).

**Recommended: Configure subscribers in `Init()`, use global `Track()` function:**

```go
import (
    "github.com/jagreehal/autotel-go"
    "github.com/jagreehal/autotel-go/subscribers"
)

cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithSubscribers(
        subscribers.NewPostHogSubscriber("phc_..."),
    ),
)
defer cleanup()

// Use the global Track function
ctx, span := autotel.Start(ctx, "userAction")
autotel.Track(ctx, "user_signed_up", map[string]any{
    "user_id": "123",
    "plan":    "premium",
})
```

**Manual queue creation (for advanced use cases):**

```go
import "github.com/jagreehal/autotel-go/subscribers"

// Option 1: PostHog
queue := subscribers.NewQueue(
    subscribers.NewPostHogSubscriber("your-posthog-api-key"),
)
defer queue.Shutdown(context.Background())

// Option 2: Mixpanel
queue := subscribers.NewQueue(
    subscribers.NewMixpanelSubscriber("your-mixpanel-token"),
)

// Option 3: Amplitude
queue := subscribers.NewQueue(
    subscribers.NewAmplitudeSubscriber("your-amplitude-api-key"),
)

// Option 4: Webhook (for any service)
queue := subscribers.NewQueue(
    subscribers.NewWebhookSubscriber("https://api.example.com",
        subscribers.WithWebhookHeaders(map[string]string{
            "Authorization": "Bearer your-api-key",
        }),
    ),
)

// Track events (automatically enriched with trace_id and span_id)
ctx, span := autotel.Start(ctx, "userAction")
queue.Track(ctx, "user_signed_up", map[string]any{
    "user_id": "123",
    "plan":    "premium",
})
```

Every event automatically includes `trace_id` and `span_id` in the properties.

## Production Hardening

### Adaptive Sampling

```go
cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithAdaptiveSampler(
        sampling.WithBaselineRate(0.1), // 10% baseline
        sampling.WithErrorRate(1.0),    // 100% errors
    ),
)
```

### Rate Limiting

```go
cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithRateLimit(100, 200), // 100 spans/sec, burst of 200
)
```

### Circuit Breaker

```go
cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithCircuitBreaker(5, 3, 30*time.Second), // 5 failures, 3 successes, 30s timeout
)
```

### PII Redaction

```go
cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithPIIRedaction(
        redaction.WithAllowlistKeys("user_id", "order_id"),
    ),
)
```

## Complete Feature List

### Core Features

- ✅ One-line initialization with environment variable support
- ✅ Ergonomic `Start()` and `Trace()` helpers
- ✅ Convenience helpers (`SetAttribute()`, `GetTraceID()`, etc.)
- ✅ Context-aware span operations

### Events & Subscribers

- ✅ `subscribers.NewQueue()` → sends to subscribers (PostHog, Mixpanel, Amplitude, Webhook, etc.)
- ✅ Global `Track()` function
- ✅ Auto-enrichment with trace context
- ✅ Queue-based event system

### Logging

- ✅ Structured logging integration with `log/slog`
- ✅ Automatic trace context injection
- ✅ Zero configuration

### Production Features

- ✅ Adaptive sampling (10% baseline, 100% errors/slow)
- ✅ Rate limiting (token bucket)
- ✅ Circuit breaker (subscriber protection)
- ✅ PII redaction (email, phone, SSN, credit card, API keys)

### Framework Integrations

- ✅ HTTP middleware (net/http)
- ✅ Gin middleware
- ✅ gRPC instrumentation

### Testing

- ✅ InMemorySpanExporter for unit tests
- ✅ Test helpers in `testing/` package

## Examples

See the `examples/` directory for complete working examples:

- `basic/` - Basic tracing usage
- `http-server/` - HTTP server with middleware
- `gin-server/` - Gin framework integration
- `logging/` - Structured logging integration
- `analytics/` - Event tracking (in-memory subscriber)
- `analytics-posthog/` - PostHog event integration
- `production-ready/` - Complete production setup with all hardening features

Run any example:

```bash
cd examples/basic
# Debug spans print to stderr and no backend is required
AUTOTEL_DEBUG=true go run main.go
```

## Debug Mode

Enable debug mode to see all span operations logged to stderr:

```go
// Option 1: Via environment variable
// AUTOTEL_DEBUG=true go run main.go

// Option 2: Programmatically
cleanup, err := autotel.Init(ctx,
    autotel.WithService("my-service"),
    autotel.WithDebug(true), // Enable debug mode
)
```

Debug output shows:

- Span creation with trace_id and span_id
- Attribute setting
- Error recording
- Span completion
- PII redaction (when enabled)

Example debug output:

```
[autotel] Debug mode enabled
[autotel] Using AlwaysSample sampler for debug mode
[autotel] → Start span: ProcessOrder [trace_id=7130bb6d5bb4ef40..., span_id=6bf9a60ec0080bff]
[autotel]   Set attribute: order.id=12345 [trace_id=7130bb6d5bb4ef40...]
[autotel] ← End span [trace_id=7130bb6d5bb4ef40..., span_id=6bf9a60ec0080bff]
```

## Convenience Helpers

Simple functions for common operations without needing to get the span first:

```go
import "github.com/jagreehal/autotel-go"

// Set single attribute on current span
autotel.SetAttribute(ctx, "user.id", "123")

// Set multiple attributes at once
autotel.SetAttributes(ctx, map[string]any{
    "order.id":    orderID,
    "order.total": total,
    "customer.tier": "premium",
})

// Add a span event
autotel.AddEvent(ctx, "order.validated", map[string]any{
    "validator": "schema_v2",
})

// Record exception (sets span status to ERROR)
if err != nil {
    autotel.RecordError(ctx, err, map[string]any{
        "order.id": orderID,
    })
}

// Get IDs for logging
traceID := autotel.GetTraceID(ctx)
spanID := autotel.GetSpanID(ctx)
log.Printf("Processing in trace: %s, span: %s", traceID, spanID)

// Track operation duration (requires span)
ctx, span := autotel.Start(ctx, "operation")
start := time.Now()
// ... do work ...
autotel.SetDuration(span, start)
span.End()

// Set HTTP request attributes (requires span)
ctx, span := autotel.Start(ctx, "httpRequest")
autotel.SetHTTPRequestAttributes(span, r.Method, r.URL.Path, r.UserAgent())
span.End()

// Add event with attributes (alternative API, requires span)
ctx, span := autotel.Start(ctx, "operation")
autotel.AddEventWithAttributes(span, "cache_hit",
    "cache.key", "user:123",
    "cache.ttl", 3600,
)
span.End()

// Check if tracing is enabled
if autotel.IsTracingEnabled(ctx) {
    // Tracing is active
}
```

**Available helpers:**

- `SetAttribute(ctx, key, value)` - Set single span attribute on current span
- `SetAttributes(ctx, attrs)` - Set multiple span attributes on current span
- `AddEvent(ctx, name, attrs)` - Add span event to current span
- `RecordError(ctx, err, attrs)` - Record exception and set error status on current span
- `GetTraceID(ctx)` - Get current trace ID as hex string
- `GetSpanID(ctx)` - Get current span ID as hex string
- `SetDuration(span, start)` - Set operation duration (requires span)
- `SetHTTPRequestAttributes(span, method, path, userAgent)` - Set HTTP attributes (requires span)
- `AddEventWithAttributes(span, name, ...)` - Add event with variadic attributes (requires span)
- `IsTracingEnabled(ctx)` - Check if tracing is active

## Status

Production ready. All core features implemented and tested.

**Version:** 1.0.0
**Go:** 1.21+
**License:** MIT

## Version

Current version: `v1.0.0`

```go
import "github.com/jagreehal/autotel-go"

version := autotel.GetVersion()
```

## Dependencies

This library uses the latest stable versions of dependencies compatible with Go 1.21+:

- **OpenTelemetry**: v1.38.0 (latest stable)
- **OpenTelemetry Contrib**: v0.63.0 (latest)
- **Gin**: v1.11.0 (latest)
- **Testify**: v1.11.1 (latest)
- **gRPC**: v1.75.0 (latest compatible with Go 1.23)

All dependencies are kept up-to-date and verified for compatibility.

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Acknowledgments

Built on top of the excellent [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) project.
