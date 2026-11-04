---
name: autotel-go
description: >
  Instruments Go services with autotel-go: Init, spans, slog logging, HTTP, gRPC,
  database and GenAI helpers, wide events, sampling and PII redaction. Use when
  adding OpenTelemetry to a Go service, calling autotel.Init, choosing between
  autotel APIs, or testing emitted spans.
---

# autotel for Go

`autotel-go` wraps the OpenTelemetry SDK. Call `Init` once, start spans with `Start` or `Trace`, and traces, metrics and logs go out over OTLP.

Add `github.com/jagreehal/autotel-go/v2` as the only telemetry dependency. `Init` builds the exporters and providers and installs W3C propagation, so skip SDK setup code.

## Setup

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cleanup, err := autotel.Init(ctx, autotel.WithService("orders"))
    if err != nil {
        log.Fatal(err)
    }
    defer cleanup() // flushes; losing this loses the last batch

    // run the service until ctx is done
}
```

- Name and version come from build info if `WithService` is left out.
- Precedence is option, then YAML, then environment. `OTEL_{TRACES,METRICS,LOGS}_EXPORTER=none` always applies.
- Variables: `OTEL_EXPORTER_OTLP_ENDPOINT` (per-signal forms too), `_PROTOCOL` (`http/protobuf`, `http/json`, `grpc`), `_HEADERS`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`. A bad protocol or sampler fails `Init`.
- Use a full `https://` URL for anything with credentials. The scheme decides TLS. A bare `host:port` is plaintext unless `WithInsecure(false)`.
- `Init` logs where telemetry goes. `WithDevtools()` sends it to `npx autotel-devtools` on `127.0.0.1:4318`.
- Every trace is kept until you choose a sampler.
- Debug prints spans to stderr and is on unless `ENV` or `ENVIRONMENT` is `production`.

## Which API

| Need | API |
| --- | --- |
| Span around work | `ctx, span := autotel.Start(ctx, "name"); defer span.End()` |
| Span that records a returned error | `autotel.Trace(ctx, "name", fn)` |
| Attributes and events on the current span | `autotel.SetAttribute(ctx, k, v)`, `autotel.AddEvent(ctx, name, attrs)` |
| Trace id for a response or log | `autotel.GetTraceID(ctx)` |
| Logs linked to spans | `autotel.Logger("name").InfoContext(ctx, ...)`, or `slog.SetDefault` with it |
| Log files | `WithLogFile(autotel.LogFile{Dir: "logs"})` or `AUTOTEL_LOG_FILE_DIR` |
| Incoming HTTP | `middleware.HTTPMiddleware("svc")(mux)`, wrapping the `ServeMux` directly |
| Chi, Gin, Echo | `middleware.ChiMiddlewareOn`, `GinMiddleware`, `EchoMiddleware` |
| Keep probes out of traces | `middleware.WithSkipPaths("/healthz")` |
| Trace id for callers | `middleware.WithTraceResponseHeader()` |
| Caller's IP behind a proxy | `middleware.WithTrustForwardedFor()`, only if the proxy overwrites the header |
| Incoming and outgoing gRPC | `middleware.GRPCServerHandler()`, `GRPCClientHandler()` with `grpc.StatsHandler` |
| Outgoing HTTP with propagation | `middleware.NewHTTPClient()` or `NewHTTPTransport(base)` |
| Database call | `database.Trace(ctx, database.Call{...}, fn)`; set `Query` and literals are removed |
| Transaction | `database.WithTransaction(ctx, db, call, fn)` |
| Connection pool metrics | `database.RegisterPoolMetrics(db, "main")` after `Init` |
| LLM calls | `genai.Trace(ctx, genai.Chat(provider, model), fn)` |
| Queues, dead letter queues, Kafka groups | `messaging` package |
| Sagas | `workflow` package; a failed step's own compensation doesn't run |
| One wide event per request | `autotel.NewRequestLogger(ctx)`, then `EmitNow` |
| Errors with context for humans | `autotel.StructuredError` with `Why`, `Fix`, `Link` |
| Product events | `autotel.Track(ctx, name, props)` with `WithSubscribers(...)` |
| Baggage | `autotel.SetBaggage(ctx, k, v)`; `WithBaggageAttributes()` copies it onto spans |
| Keep one request | `WithDebugCapture()` and `autotel.DebugBaggageKey` baggage |
| Sampling | `WithAdaptiveSampler(...)`, `WithTargetRateSampler(...)`, `OTEL_TRACES_SAMPLER` |
| Load shedding | `WithRateLimit(rate, burst)` caps new traces; `WithCircuitBreaker(failures, successes, timeout)` stops new traces while exports fail |
| PII | `WithPIIRedaction(...)` |
| Metrics | `autotel.Meter().Counter(ctx, name, 1, attrs)`, `WithRuntimeMetrics()` |
| Vendors | `backends` package |
| SLOs and cohorts | `slo` and `analysis` packages |

## Goroutines

The span travels in `ctx`. Pass it to the goroutine. Don't use a request's `ctx` for work that outlives the request: its cancellation stops the work, so copy the span with `trace.ContextWithSpanContext(context.Background(), span.SpanContext())`.

## Pitfalls

- Span names and metric attributes must stay low-cardinality: IDs go on span attributes, never in the name.
- Put the middleware directly around the `ServeMux`. A wrapper that calls `r.WithContext` in between hides the route.
- Don't record prompts, raw SQL or request bodies. `genai` and `database` leave them out for you.

## Testing

```go
exporter, cleanup := autoteltesting.SetupTest(t) // github.com/jagreehal/autotel-go/v2/testing
defer cleanup()

runCodeUnderTest(ctx)

span, ok := exporter.GetSpanByName("order.create")
```
