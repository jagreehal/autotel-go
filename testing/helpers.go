package testing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// SetupTest initializes a test tracer provider with an in-memory exporter.
// Returns the exporter and a cleanup function.
func SetupTest(t interface{ Helper() }) (*InMemoryExporter, func()) {
	if t != nil {
		t.Helper()
	}

	exporter := NewInMemoryExporter()

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName("test-service")),
	)
	if err != nil {
		panic(err)
	}

	tp := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)),
		trace.WithSampler(trace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)

	cleanup := func() {
		_ = tp.Shutdown(context.Background())
		exporter.Reset()
	}

	return exporter, cleanup
}
