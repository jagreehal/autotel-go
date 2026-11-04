# autotel-go

[![Go Reference](https://pkg.go.dev/badge/github.com/jagreehal/autotel-go/v2.svg)](https://pkg.go.dev/github.com/jagreehal/autotel-go/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/jagreehal/autotel-go/v2)](https://goreportcard.com/report/github.com/jagreehal/autotel-go/v2)

Add OpenTelemetry to a Go service with one function call. `autotel.Init` sets up traces, metrics and logs, and sends them to Grafana, Honeycomb, Datadog, Jaeger or any other backend that speaks OTLP.

## What you get

OpenTelemetry (OTel) is the open standard for reporting what your code does. It gives you three kinds of data:

| Data | Answers | Example |
| --- | --- | --- |
| Traces | Where did this request spend its time? | `checkout` took 850ms, and `charge card` took 800ms of that |
| Metrics | How is the whole service doing? | 12 errors a minute, 300ms at p95 |
| Logs | What happened at this moment? | `order placed`, linked to the trace it belongs to |

A **trace** is a tree of **spans**, and a span is one timed step of the work. **OTLP** is the format that carries all three to your backend.

Wiring this up by hand takes [90 lines and eight modules](docs/manual-setup.md). With autotel you write one call.

## See your first trace

You need Go 1.26 and Node.js. Allow five minutes.

**1. Start a local viewer** and leave it running:

```sh
npx autotel-devtools
```

**2. Create a project** in a second terminal:

```sh
mkdir hello && cd hello
go mod init hello
go get github.com/jagreehal/autotel-go/v2
```

**3. Save this as `main.go`:**

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/jagreehal/autotel-go/v2"
)

func main() {
	ctx := context.Background()

	cleanup, err := autotel.Init(ctx, autotel.WithService("hello"), autotel.WithDevtools())
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup() // sends anything still buffered before the program exits

	checkout(ctx)
}

func checkout(ctx context.Context) {
	ctx, span := autotel.Start(ctx, "checkout")
	defer span.End()

	span.SetAttribute("order.id", "ord_42")
	autotel.Logger("hello").InfoContext(ctx, "order placed")
	charge(ctx)
}

func charge(ctx context.Context) {
	_, span := autotel.Start(ctx, "charge card")
	defer span.End()

	time.Sleep(50 * time.Millisecond) // pretend to call a payment provider
}
```

**4. Run it** with `go run .`, then open <http://127.0.0.1:4318>.

You'll see a `checkout` trace with `charge card` nested inside it, and the `order placed` log attached.

One rule makes the nesting work: `Start` returns a new `ctx`, and you pass that `ctx` down. Go code already does this for cancellation, so tracing usually fits the functions you have.

## Add it to your service

Call `Init` once in `main`. Shut down on SIGTERM so the last batch gets sent:

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cleanup, err := autotel.Init(ctx, autotel.WithService("orders"))
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// run your service until ctx is done
}
```

Leave out `WithService` and the name and version come from your binary's build info. At startup, `Init` logs where your telemetry is going.

### Trace a function

`Trace` opens and closes the span for you, and records a returned error:

```go
user, err := autotel.Trace(ctx, "load user", func(ctx context.Context, span autotel.Span) (*User, error) {
	span.SetAttribute("user.id", id)
	return db.FindUser(ctx, id)
})
```

Put IDs in attributes and keep them out of span names, so your backend can group the spans.

### Trace an HTTP server

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /users/{id}", handleUser)

handler := middleware.HTTPMiddleware("orders",
	middleware.WithSkipPaths("/healthz"), // keep probes out of your traces
)(mux)
http.ListenAndServe(":8080", handler)
```

Each request gets a span named after its route, such as `GET /users/{id}`, plus latency metrics. Using Chi, Gin or Echo? See [HTTP and gRPC](docs/http-and-grpc.md).

### Call other services

Use the traced client and the next service joins the same trace:

```go
client := middleware.NewHTTPClient()
resp, err := client.Get(ctx, "https://api.example.com/users")
```

### Write logs

```go
logger := autotel.Logger("orders")
logger.InfoContext(ctx, "order placed", "order.id", id)
```

Use the `...Context` methods. They attach the trace ID, so your backend can jump from a log line to its trace. With nowhere to export, logs print to stderr. To send all your `slog` output this way, call `slog.SetDefault(logger)`.

### Record metrics

```go
autotel.Meter().Counter(ctx, "orders.placed", 1, map[string]any{"plan": "pro"})
autotel.Meter().Histogram(ctx, "checkout.total", 42.5, nil)
```

Keep metric attributes to a small set of values. Each new value starts a new time series, so `plan` works and `user.id` doesn't.

### Trace database and LLM calls

```go
err := database.Trace(ctx, database.Call{
	System: "postgresql", Operation: "SELECT", Namespace: "shop", Collection: "users",
	Query: query, // recorded with its values removed
}, func(ctx context.Context) error {
	return db.QueryRowContext(ctx, query, id).Scan(&name)
})
```

`genai.Trace` does the same for LLM calls and records token usage. See [Databases and LLM calls](docs/database-and-llm.md).

### Test your telemetry

```go
import autoteltesting "github.com/jagreehal/autotel-go/v2/testing"

func TestCheckout(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	checkout(context.Background())

	if _, ok := exporter.GetSpanByName("checkout"); !ok {
		t.Fatal("no checkout span")
	}
}
```

## Send it to a backend

You change environment variables and leave your code alone.

**Grafana Cloud** (find these values under *OpenTelemetry* in your Grafana Cloud stack):

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64 instance:token>"
```

**Jaeger** stores traces only, so turn the other two off:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export OTEL_METRICS_EXPORTER=none
export OTEL_LOGS_EXPORTER=none
```

Use an `https://` URL for anything that needs credentials. For gRPC, YAML, per-signal endpoints and the Datadog and Honeycomb presets, see [Configuration](docs/configuration.md).

## When something's wrong

| You see | Do this |
| --- | --- |
| `no OTLP endpoint, telemetry stays local` | Set `OTEL_EXPORTER_OTLP_ENDPOINT`. |
| `telemetry not delivered` with `401` | Check your credentials in `OTEL_EXPORTER_OTLP_HEADERS`. |
| `telemetry not delivered` with `404` | Check the URL. If your backend skips a signal, as Jaeger does, set `OTEL_METRICS_EXPORTER=none` or `OTEL_LOGS_EXPORTER=none`. |
| `connection refused` | Start your backend, or check the port. |
| A short program sends nothing | Call `cleanup()` before it exits. `log.Fatal` and `os.Exit` skip your `defer`. |
| Spans show up as separate traces | Pass the `ctx` from `Start` down to the next call. |
| HTTP spans say `GET` with no route | Put `HTTPMiddleware` directly around your `ServeMux`. A middleware between them that calls `r.WithContext` hides the route. |
| Span details print to stderr | That's debug mode, on unless `ENV` or `ENVIRONMENT` is `production`. Turn it off with `autotel.WithDebug(false)`. |

## Before you go to production

- **Set `ENV=production`** to turn off debug output.
- **Pick a sampling rate.** autotel keeps every trace until you choose. `OTEL_TRACES_SAMPLER=parentbased_traceidratio` with `OTEL_TRACES_SAMPLER_ARG=0.1` keeps 10%. `WithAdaptiveSampler()` keeps a share of normal traces and every error and slow span.
- **Add `autotel.WithPIIRedaction()`** to scrub emails, phone numbers and card numbers from spans, logs, metrics and product events.
- **Add `autotel.WithRuntimeMetrics()`** to see memory, garbage collection and goroutines.

[Running in production](docs/production.md) covers these, plus rate limits, circuit breakers and log files.

## Learn more

- [Workshop](workshop/README.md): thirteen parts that take a bank's ATM from `fmt.Println` to one trace across three services. Start here if you want to learn observability itself.
- [HTTP and gRPC](docs/http-and-grpc.md): Chi, Gin, Echo, gRPC and HTTP client options.
- [Databases and LLM calls](docs/database-and-llm.md)
- [Queues, workflows and events](docs/event-driven.md): Kafka and SQS tracing, sagas and business baggage.
- [Advanced features](docs/advanced.md): product analytics, SLOs, wide events and structured errors.
- [Configuration](docs/configuration.md) and [Running in production](docs/production.md)
- [`examples/`](examples/) has runnable programs. [API reference](https://pkg.go.dev/github.com/jagreehal/autotel-go/v2).

Using a coding assistant? `npx skills add jagreehal/autotel-go --skill autotel-go` teaches it this API.

[Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [Changelog](CHANGELOG.md) · [MIT License](LICENSE)
