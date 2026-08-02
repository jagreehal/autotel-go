package processors

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestFilteringSpanProcessor_DropsWhenPredicateFalse(t *testing.T) {
	exporter := autoteltesting.NewInMemoryExporter()
	next := sdktrace.NewSimpleSpanProcessor(exporter)
	p := NewFilteringSpanProcessor(func(s sdktrace.ReadOnlySpan) bool {
		return s.Name() != "drop-me"
	}, next)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx := context.Background()

	_, span1 := tracer.Start(ctx, "keep-me")
	span1.End()

	_, span2 := tracer.Start(ctx, "drop-me")
	span2.End()

	// Force flush so the processor exports
	_ = p.ForceFlush(ctx)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name() != "keep-me" {
		t.Errorf("expected span name keep-me, got %s", spans[0].Name())
	}
}
