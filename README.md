# autotel-go

<div align="center">

[![Go Reference](https://pkg.go.dev/badge/github.com/jagreehal/autotel-go/v2.svg)](https://pkg.go.dev/github.com/jagreehal/autotel-go/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/jagreehal/autotel-go/v2)](https://goreportcard.com/report/github.com/jagreehal/autotel-go/v2)

</div>

OpenTelemetry instrumentation for Go.

- One-line initialization with `Init()` and `Start()` helpers
- OTLP-first design with subscribers for PostHog, Mixpanel, Amplitude, Webhook, and custom destinations
- Production features: adaptive sampling, rate limiting, circuit breakers, PII redaction
- Automatic enrichment: trace context flows into spans, logs, and events

OpenTelemetry requires significant boilerplate. Autotel provides a simpler API while maintaining full control over your telemetry.

```bash
go get github.com/jagreehal/autotel-go/v2
```

## Quick Start

### 1. Initialize once at startup

```go
import "github.com/jagreehal/autotel-go/v2"

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
- Vendor presets (OTLP-first): `WithBackend("datadog"|"honeycomb"|"grafana")` sets endpoint + headers; use `WithHeaders` for API keys/datasets without extra SDKs.
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
import "github.com/jagreehal/autotel-go/v2"

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
    "github.com/jagreehal/autotel-go/v2"
    "github.com/jagreehal/autotel-go/v2/subscribers"
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
import "github.com/jagreehal/autotel-go/v2"

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
- ✅ **Framework integrations** - HTTP server/client, gRPC server/client, Gin middleware
- ✅ **Event-driven tracing** - Message queues, workflows/sagas, business baggage
- ✅ **SLO tracking** - Rolling SLIs, error-budget burn alerts, and predictive forecasts
- ✅ **Vendor lock-in free** - Uses standard OpenTelemetry, works with any OTLP backend

## Comparison

| Feature             | Raw OpenTelemetry                 | autotel-go                |
| ------------------- | --------------------------------- | ------------------------- |
| Initialization      | 20-30 lines                       | 1 line (`Init()`)         |
| Span creation       | `tracer.Start()` + manual `End()` | `Start()` with defer      |
| Error recording     | Manual                            | Automatic with `Trace()`  |
| Adaptive sampling   | ❌ (collector only)               | ✅ Built-in               |
| Rate limiting       | ❌                                | ✅ Built-in               |
| PII redaction       | ❌                                | ✅ Built-in               |
| Product events      | ❌                                | ✅ Built-in (subscribers) |
| HTTP middleware     | Manual setup                      | `HTTPMiddleware()`        |
| HTTP client         | Manual propagation                | `NewHTTPClient()`         |
| Message queues      | Manual spans + propagation        | `messaging.Consumer/Producer` |
| Workflow/Saga       | ❌                                | `workflow.New()` with compensations |
| Business baggage    | Manual + no guardrails            | `baggage.New()` with PII hashing |
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
    "github.com/jagreehal/autotel-go/v2/middleware"
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
    "github.com/jagreehal/autotel-go/v2/middleware"
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
    "github.com/jagreehal/autotel-go/v2/middleware"
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

### Service-to-Service HTTP Calls

When making HTTP requests to other services, trace context must be propagated via W3C `traceparent` headers for distributed tracing to work.

**Option 1: TracedHTTPClient (recommended)**

```go
import "github.com/jagreehal/autotel-go/v2/middleware"

// Create a traced client - just works!
client := middleware.NewHTTPClient()

// All requests automatically include traceparent headers
resp, err := client.Get(ctx, "https://api.example.com/users")
resp, err := client.Post(ctx, "https://api.example.com/orders", "application/json", jsonData)
resp, err := client.Put(ctx, "https://api.example.com/users/123", "application/json", jsonData)
resp, err := client.Delete(ctx, "https://api.example.com/users/123")
```

**Option 2: Wrap existing client**

```go
// Wrap an existing client while preserving its settings
existingClient := &http.Client{Timeout: 60 * time.Second}
tracedClient := middleware.WrapHTTPClient(existingClient)

resp, err := tracedClient.Get(ctx, "https://api.example.com/users")
```

**Option 3: Use the transport directly**

```go
// For custom HTTP client configurations
client := &http.Client{
    Transport: middleware.NewHTTPTransport(&http.Transport{
        MaxIdleConns:    100,
        IdleConnTimeout: 90 * time.Second,
    }),
    Timeout: 30 * time.Second,
}

req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := client.Do(req)
```

**Option 4: Manual header injection**

```go
// For cases where you can't change the client
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
middleware.InjectHeaders(ctx, req)
resp, err := http.DefaultClient.Do(req)
```

**Configuration options:**

```go
client := middleware.NewHTTPClient(
    middleware.WithTimeout(10 * time.Second),           // Request timeout
    middleware.WithSpanNameFormatter(func(req *http.Request) string {
        return fmt.Sprintf("HTTP %s %s", req.Method, req.URL.Host)
    }),
    middleware.WithoutSpans(),                          // Propagate headers without creating spans
    middleware.WithResponseStatus(),                    // Record HTTP status in span (default: on)
)
```

**What gets propagated:**

- `traceparent` header (W3C Trace Context)
- `tracestate` header (vendor-specific context)
- `baggage` header (cross-cutting concerns)

## Event-Driven Tracing

### Message Queue Tracing

Trace message producers and consumers with automatic link-based context propagation:

```go
import "github.com/jagreehal/autotel-go/v2/messaging"

// Producer - publishes messages with trace context
producer := messaging.NewProducer(
    messaging.WithProducerSystem(messaging.SystemKafka),
    messaging.WithProducerDestination("orders"),
)

err := producer.Publish(ctx, &msg, func(ctx context.Context) error {
    return kafkaProducer.Send(msg)
})

// Consumer - processes messages with automatic link extraction
consumer := messaging.NewConsumer(
    messaging.WithSystem(messaging.SystemKafka),
    messaging.WithDestination("orders"),
    messaging.WithConsumerGroup("order-processor"),
    messaging.WithLinks(), // Links mode (default) - creates new trace linked to producer
)

err := consumer.Process(ctx, msg, func(ctx context.Context, span trace.Span) error {
    return processOrder(ctx, msg)
})
```

**Batch processing with fan-in links:**

```go
processor := messaging.NewBatchProcessor(
    messaging.WithBatchSystem(messaging.SystemKafka),
    messaging.WithBatchDestination("events"),
)

// Process entire batch (creates links to all producer spans)
err := processor.Process(ctx, messages, func(ctx context.Context, span trace.Span) (int, error) {
    return processBatch(ctx, messages)
})
```

**DLQ and retry tracking:**

```go
// Record when message is sent to Dead Letter Queue
messaging.RecordDLQ(ctx, messaging.DLQInfo{
    OriginalDestination: "orders",
    DLQDestination:      "orders.dlq",
    Reason:              "max_retries_exceeded",
    RetryCount:          3,
})

// Record retry attempts
messaging.RecordRetry(ctx, messaging.RetryInfo{
    Attempt:     2,
    MaxAttempts: 3,
    BackoffMs:   1000,
})

// Record consumer lag metrics
messaging.RecordConsumerLag(ctx, messaging.ConsumerLagInfo{
    LagMs:       1500,
    LagMessages: 100,
    Partition:   0,
})
```

### Workflow/Saga Tracing

Trace distributed transactions with automatic compensation on failure:

```go
import "github.com/jagreehal/autotel-go/v2/workflow"

wf := workflow.New(ctx, "order-fulfillment")

wf.Step("validate", func(ctx context.Context, span trace.Span) error {
    return validateOrder(ctx, order)
})

wf.Step("charge", func(ctx context.Context, span trace.Span) error {
    return chargeCustomer(ctx, order)
}, workflow.WithCompensation(func(ctx context.Context, span trace.Span) error {
    return refundCustomer(ctx, order) // Runs automatically if later step fails
}))

wf.Step("ship", func(ctx context.Context, span trace.Span) error {
    return shipOrder(ctx, order)
}, workflow.WithCompensation(func(ctx context.Context, span trace.Span) error {
    return cancelShipment(ctx, order)
}))

// Run workflow - compensations run in reverse order on failure
if err := wf.Run(ctx); err != nil {
    // charge failed? → refund runs
    // ship failed? → cancelShipment runs, then refund runs
    return err
}
```

### Business Baggage (Safe Context Propagation)

Propagate business context across services with PII protection:

```go
import "github.com/jagreehal/autotel-go/v2/baggage"

// Configure allowed keys and PII handling
bc := baggage.New(
    baggage.WithAllowedKeys("tenant_id", "correlation_id", "user_tier"),
    baggage.WithHashKeys("user_id", "email"),  // Auto-hash PII
    baggage.WithMaxValueLength(256),
)

// Set baggage (PII is automatically hashed)
ctx, _ = bc.Set(ctx, "tenant_id", "acme-corp")
ctx, _ = bc.Set(ctx, "user_id", "user@example.com") // → stored as SHA256 hash

// Quick helper for multiple values
ctx = baggage.WithBusinessContext(ctx,
    "tenant_id", "acme-corp",
    "correlation_id", "abc-123",
)
```

### Consumer Group Tracking

Track Kafka consumer group lifecycle events:

```go
import "github.com/jagreehal/autotel-go/v2/messaging"

tracker := messaging.NewConsumerGroupTracker(
    messaging.WithConsumerGroupID("order-processor"),
    messaging.WithMemberID("consumer-1"),
    messaging.WithOnRebalance(func(event messaging.RebalanceEvent) {
        log.Printf("Rebalance: %s", event.Type)
    }),
)

// Record rebalance events
tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
    Type:       messaging.RebalanceAssigned,
    Partitions: []messaging.PartitionAssignment{{Topic: "orders", Partition: 0}},
    Timestamp:  time.Now(),
    Generation: 5,
})

// Record heartbeat health
tracker.RecordHeartbeat(ctx, true, 5*time.Millisecond)

// Record partition lag
tracker.RecordPartitionLag(ctx, messaging.PartitionLagInfo{
    Topic:         "orders",
    Partition:     0,
    CurrentOffset: 1000,
    EndOffset:     1050,
    Lag:           50,
})
```

### Message Ordering & Deduplication

Track message ordering and detect duplicates:

```go
import "github.com/jagreehal/autotel-go/v2/messaging"

tracker := messaging.NewOrderingTracker(
    messaging.WithDeduplicationWindowSize(5000),
    messaging.WithDeduplicationWindowDuration(10*time.Minute),
    messaging.WithOnDuplicate(func(ctx context.Context, msgID string) {
        log.Printf("Duplicate message: %s", msgID)
    }),
)

result := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
    ID:        msg.ID(),
    Sequence:  msg.Offset,
    Partition: msg.Partition,
    Topic:     msg.Topic,
})

switch result {
case messaging.OrderingDuplicate:
    return nil // Skip duplicate
case messaging.OrderingOutOfOrder:
    log.Printf("Out of order message")
case messaging.OrderingGap:
    log.Printf("Gap detected - missing messages")
}
```

### Enhanced DLQ with Reason Categories

Categorize DLQ routing for better filtering:

```go
import "github.com/jagreehal/autotel-go/v2/messaging"

// Record with category
messaging.RecordDLQ(ctx, messaging.DLQInfo{
    QueueName:       "orders-dlq",
    ReasonCategory:  messaging.DLQReasonValidation, // validation, timeout, poison, etc.
    OriginalMessageID: msg.ID(),
    ProducerHeaders: msg.Headers(), // For linking to producer span
    DwellTimeMs:     1500,
})

// Auto-classify errors
category := messaging.ClassifyDLQReason(err) // Returns appropriate category

// Track DLQ replays
messaging.RecordDLQReplay(ctx, messaging.DLQReplayInfo{
    SourceDLQ:     "orders-dlq",
    TargetTopic:   "orders",
    MessageID:     msg.ID(),
    ReplayAttempt: 1,
    ReplayedBy:    "dlq-replay-service",
})
```

### Webhook/Parking Lot Pattern

Trace async operations that complete hours/days later (webhooks, payment callbacks):

```go
import "github.com/jagreehal/autotel-go/v2/webhook"

store := webhook.NewInMemoryStore()
lot := webhook.NewParkingLot(store, webhook.WithDefaultTTL(24*time.Hour))

// When initiating payment
func initiatePayment(ctx context.Context, orderID string) error {
    ctx, span := tracer.Start(ctx, "initiate-payment")
    defer span.End()

    // Park trace context before async call
    err := lot.Park(ctx, "payment:"+orderID, webhook.WithMetadata(map[string]string{
        "order_id": orderID,
    }))
    if err != nil {
        return err
    }

    return stripe.CreatePaymentIntent(orderID)
}

// When webhook arrives (hours later)
func handleWebhook(ctx context.Context, event StripeEvent) error {
    orderID := event.Data.Object.Metadata["order_id"]

    // Retrieve parked context and create linked span
    ctx, span, sc := lot.RetrieveAndTrace(ctx, "payment:"+orderID, "stripe.webhook.payment_succeeded")
    defer span.End()

    if sc != nil {
        log.Printf("Payment completed after %dms", sc.ElapsedMs())
    }

    return fulfillOrder(ctx, orderID)
}
```

### Safe Baggage Schema

Type-validated baggage with PII detection:

```go
import "github.com/jagreehal/autotel-go/v2/baggage"

schema := baggage.NewSchema(
    baggage.WithMaxTotalSize(8192),
    baggage.WithStrictMode(true),
).
    DefineStringField("tenant_id", 64, true).           // Required, max 64 chars
    DefineHashedField("user_id").                       // Auto-hash for privacy
    DefineEnumField("priority", []string{"low", "normal", "high"}).
    DefinePIIField("notes")                              // Auto-detect & redact PII

sb := baggage.NewSafeBaggage(schema)

// Values are validated and transformed
ctx, err := sb.Set(ctx, "tenant_id", "acme-corp")
ctx, err := sb.Set(ctx, "user_id", "user@example.com") // Stored as hash
ctx, err := sb.Set(ctx, "priority", "high")

// Check for PII
detected := schema.DetectPII("Contact: john@example.com")
// Returns: ["email"]

// Validate required fields
missing := sb.CheckRequiredFields(ctx)
```

### Workflow Step Linking & Retry

Link workflow steps and configure retry behavior:

```go
import "github.com/jagreehal/autotel-go/v2/workflow"

wf := workflow.New(ctx, "order-fulfillment")

wf.Step("validate", validateOrder)

wf.Step("charge", chargeCustomer,
    workflow.WithLinkToPrevious(),           // Link to validate step
    workflow.WithRetry(workflow.RetryConfig{
        MaxAttempts: 3,
        BackoffMs:   100,
        Multiplier:  2.0,
        MaxBackoff:  5000,
    }),
    workflow.WithIdempotent(),               // Mark as safe to retry
    workflow.WithCompensation(refundCustomer),
)

wf.Step("ship", shipOrder,
    workflow.WithLinkTo("validate", "charge"), // Link to multiple steps
    workflow.WithCompensation(cancelShipment),
)

if err := wf.Run(ctx); err != nil {
    return err
}
```

### Links-Based Sampling

Sample consumer spans when linked to sampled producer spans:

```go
import "github.com/jagreehal/autotel-go/v2/sampling"

// Configure sampler with links-based sampling
sampler := sampling.NewAdaptiveSampler(
    sampling.WithBaselineRate(0.1),  // 10% baseline
    sampling.WithLinksBased(true),   // Enable links-based sampling
    sampling.WithLinksRate(1.0),     // 100% when linked to sampled span
)

// Create links from message headers
link, ok := sampling.CreateLinkFromHeaders(msg.Headers)
if ok {
    ctx, span := tracer.Start(ctx, "process-message", trace.WithLinks(link))
    defer span.End()
}

// Batch: extract links from multiple messages
links := sampling.ExtractLinksFromBatch(messages, func(m Message) map[string]string {
    return m.Headers
})
```

## Advanced Features

### SLO tracking and burn-rate forecasting

The `slo` package tracks good and bad events over a rolling window, emits
low-cardinality OpenTelemetry metrics, evaluates dual-window burn-rate alerts,
and forecasts whether recent failures will exhaust the error budget.

```go
import (
    "time"

    "github.com/jagreehal/autotel-go/v2/slo"
)

tracker, err := slo.NewTracker(slo.Definition{
    Name:   "checkout.availability",
    Target: 0.99,
    Window: 30 * 24 * time.Hour,
})
if err != nil {
    log.Fatal(err)
}

snapshot, err := tracker.Record(ctx, slo.OutcomeGood)
if err != nil {
    log.Fatal(err)
}

forecast, err := tracker.Forecast(slo.ForecastOptions{
    Baseline:  6 * time.Hour,
    Lookahead: 24 * time.Hour, // at most four times the baseline
})
if err != nil {
    log.Fatal(err)
}

fmt.Printf("current burn: %.2fx, forecast alerting: %t\n",
    snapshot.BurnRate, forecast.Alerting)
```

Use `slo.WithClock` for deterministic simulations and tests,
`slo.WithMetrics(false)` for calculation-only trackers, or `slo.WithMeter` to
send the `autotel.slo.outcomes` and `autotel.slo.burn_rate` instruments to a
specific meter provider.

### Structured Logging

Automatically inject trace context into logs using `log/slog`:

```go
import (
    "log/slog"
    "github.com/jagreehal/autotel-go/v2/logging"
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
    "github.com/jagreehal/autotel-go/v2"
    "github.com/jagreehal/autotel-go/v2/subscribers"
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
import "github.com/jagreehal/autotel-go/v2/subscribers"

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

The baseline is decided when a span starts, from its trace ID, so a routine trace
is kept or dropped whole rather than arriving with holes in it.

Errors and slow spans cannot be decided there: neither status nor duration exists
yet. `Init` therefore records every span in-process and applies these rates when
the span ends, which is the only point at which "keep every error" can mean
anything. Export volume still follows the rates you set; the cost is building
spans that are then dropped locally. Set `WithErrorRate` and `WithSlowRate` no
higher than the baseline to opt out and decide everything at head.

A span kept for failing may be the only survivor of an otherwise dropped trace —
a tail decision cannot be propagated back to spans that already ended.

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
- ✅ Rolling SLO tracking and predictive burn-rate alerts

### Framework Integrations

- ✅ HTTP middleware (net/http) - inbound request tracing
- ✅ HTTP client - outbound request tracing with automatic propagation
- ✅ Gin middleware
- ✅ gRPC instrumentation (server + client)

### Event-Driven Tracing

- ✅ Message queue tracing (Kafka, RabbitMQ, SQS, etc.)
- ✅ Producer/Consumer middleware with automatic context propagation
- ✅ Batch processing with fan-in links
- ✅ Consumer group tracking (rebalance, heartbeat, partition lag)
- ✅ Message ordering & deduplication (sequence tracking, gap detection)
- ✅ Enhanced DLQ with reason categories and replay tracking
- ✅ Webhook/Parking Lot pattern for async callbacks
- ✅ Workflow/Saga tracing with step linking and retry configuration
- ✅ Safe baggage schema with type validation and PII detection
- ✅ Links-based sampling for trace continuity

### Testing

- ✅ InMemorySpanExporter for unit tests
- ✅ Test helpers in `testing/` package

## Examples

See the `examples/` directory for complete working examples:

- `basic/` - Basic tracing usage
- `http-server/` - HTTP server with middleware
- `service-to-service/` - Distributed tracing between services
- `gin-server/` - Gin framework integration
- `logging/` - Structured logging integration
- `analytics/` - Event tracking (in-memory subscriber)
- `analytics-posthog/` - PostHog event integration
- `production-example/` - Complete production setup with all hardening features

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
import "github.com/jagreehal/autotel-go/v2"

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

**Version:** 2.1.0
**Go:** 1.25+ (Go 1.26.5 toolchain recommended)
**License:** MIT

## Version

Current version: `v2.1.0`

```go
import "github.com/jagreehal/autotel-go/v2"

version := autotel.GetVersion()
```

## Dependencies

This library uses the latest stable versions of dependencies compatible with Go 1.25+:

- **OpenTelemetry**: v1.44.0
- **OpenTelemetry Contrib**: v0.69.0
- **Gin**: v1.12.0
- **Testify**: v1.11.1 (latest)
- **gRPC**: v1.83.0

All dependencies are kept up-to-date and verified for compatibility.

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Acknowledgments

Built on top of the excellent [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) project.
