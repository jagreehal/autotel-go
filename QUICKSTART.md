# Quick Start Guide

Get started with autotel-go in 5 minutes.

## Installation

```bash
go get github.com/jagreehal/autotel-go
```

## 1. Initialize

```go
package main

import (
    "context"
    "log"
    "github.com/jagreehal/autotel-go"
)

func main() {
cleanup, err := autotel.Init(context.Background(),
    autotel.WithService("my-service"),
    autotel.WithEndpoint("http://localhost:4318"),
    // Optional: OTLP vendor preset without extra SDKs
    // autotel.WithBackend("datadog"),
)
    if err != nil {
        log.Fatal(err)
    }
    defer cleanup()

    // Your application code here
}
```

## 2. Add Tracing

### Option A: Using Start()

```go
func ProcessOrder(ctx context.Context, orderID string) error {
    ctx, span := autotel.Start(ctx, "ProcessOrder")
    defer span.End()

    span.SetAttribute("order.id", orderID)
    // ... your code ...
    return nil
}
```

### Option B: Using Trace()

```go
func GetUser(ctx context.Context, userID string) (*User, error) {
    return autotel.Trace(ctx, "GetUser", func(ctx context.Context, span autotel.Span) (*User, error) {
        span.SetAttribute("user.id", userID)
        return db.FindUser(ctx, userID)
    })
}
```

## 3. Add HTTP Middleware

```go
import "github.com/jagreehal/autotel-go/middleware"

mux := http.NewServeMux()
mux.HandleFunc("/users", handleUsers)

handler := middleware.HTTPMiddleware("my-service")(mux)
http.ListenAndServe(":8080", handler)
```

## 4. Add Production Features (Optional)

```go
cleanup, err := autotel.Init(context.Background(),
    autotel.WithService("my-service"),
    autotel.WithEndpoint("http://localhost:4318"),

    // Production hardening
    autotel.WithAdaptiveSampler(...),
    autotel.WithRateLimit(100, 200),
    autotel.WithCircuitBreaker(5, 3, 30*time.Second),
    autotel.WithPIIRedaction(...),
)
```

## 5. Add Analytics Events (Optional)

```go
import (
    "github.com/jagreehal/autotel-go"
    "github.com/jagreehal/autotel-go/subscribers"
)

cleanup, _ := autotel.Init(context.Background(),
    autotel.WithService("my-service"),
    autotel.WithSubscribers(
        subscribers.NewPostHogSubscriber("your-api-key"),
    ),
    autotel.WithEventQueue(2000, 500*time.Millisecond, 5),
    autotel.WithEventBackoff(100*time.Millisecond, 5*time.Second, 10*time.Second),
)
defer cleanup()

ctx, span := autotel.Start(ctx, "userAction")
defer span.End()
autotel.Track(ctx, "user_signed_up", map[string]any{
    "user_id": "123",
})
```

## 6. Emit Metrics with Trace Correlation (Optional)

```go
m := autotel.Meter()
m.Counter(ctx, "orders.created", 1, map[string]any{"region": "iad"})
m.Histogram(ctx, "orders.duration_ms", float64(time.Since(start).Milliseconds()), nil)
```

## Logging with Trace Context

- Slog: wrap your handler with `logging.NewTraceHandler(...)`.
- Zap: add `logging.TraceFields(ctx)...` to log calls.

## Next Steps

- See [examples/](examples/) for complete examples
- Read [README.md](README.md) for full documentation
- Check [ARCHITECTURE.md](ARCHITECTURE.md) for design details
