# OpenTelemetry without autotel

This program wires up by hand what one `autotel.Init` call gives you: traces, metrics, logs, propagation and shutdown on SIGTERM. It compiles against the module versions autotel uses, and it still has no redaction, sampling or middleware.

|  | By hand | autotel |
| --- | --- | --- |
| OpenTelemetry modules to add and keep on matching versions | 8 | 1 |
| Lines in `main.go`, after `gofmt` | 90 | 25 |

```go
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("orders"), semconv.ServiceVersion("1.0.0")),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithHost(),
	)
	if err != nil {
		log.Fatal(err)
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	otel.SetTracerProvider(tp)

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		log.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	otel.SetMeterProvider(mp)

	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		log.Fatal(err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)
	otel.SetLoggerProvider(lp)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger := otelslog.NewLogger("orders")
	logger.InfoContext(ctx, "started")

	// your service runs here
	<-ctx.Done()

	shutdown := context.Background()
	if err := tp.Shutdown(shutdown); err != nil {
		slog.Error("traces", "error", err)
	}
	if err := mp.Shutdown(shutdown); err != nil {
		slog.Error("metrics", "error", err)
	}
	if err := lp.Shutdown(shutdown); err != nil {
		slog.Error("logs", "error", err)
	}
}
```
