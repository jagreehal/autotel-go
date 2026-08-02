package processors

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestTailSamplingSpanProcessor_DropsWhenKeepFalse(t *testing.T) {
	exporter := autoteltesting.NewInMemoryExporter()
	next := sdktrace.NewSimpleSpanProcessor(exporter)
	p := NewTailSamplingSpanProcessor(next)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx := context.Background()

	// Span that is evaluated and kept
	_, span1 := tracer.Start(ctx, "kept")
	span1.SetAttributes(
		attribute.Bool(TailEvaluatedKey, true),
		attribute.Bool(TailKeepKey, true),
	)
	span1.End()

	// Span that is evaluated and dropped
	_, span2 := tracer.Start(ctx, "dropped")
	span2.SetAttributes(
		attribute.Bool(TailEvaluatedKey, true),
		attribute.Bool(TailKeepKey, false),
	)
	span2.End()

	_ = p.ForceFlush(ctx)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span (dropped one filtered out), got %d", len(spans))
	}
	if spans[0].Name() != "kept" {
		t.Errorf("expected span name kept, got %s", spans[0].Name())
	}
}

func TestTailSamplingSpanProcessor_AppliesPolicy(t *testing.T) {
	exporter := autoteltesting.NewInMemoryExporter()
	next := sdktrace.NewSimpleSpanProcessor(exporter)
	p := NewTailSamplingSpanProcessor(next, WithTailPolicy(func(s sdktrace.ReadOnlySpan) bool {
		return s.Name() == "keep-me"
	}))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx := context.Background()

	for _, name := range []string{"keep-me", "drop-me"} {
		_, span := tracer.Start(ctx, name)
		span.End()
	}
	_ = p.ForceFlush(ctx)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected the policy to keep 1 of 2 spans, got %d", len(spans))
	}
	if spans[0].Name() != "keep-me" {
		t.Errorf("kept %q, want keep-me", spans[0].Name())
	}
}

// Code that marked a span by hand meant it; a policy must not overrule it.
func TestTailSamplingSpanProcessor_ExplicitDecisionBeatsPolicy(t *testing.T) {
	exporter := autoteltesting.NewInMemoryExporter()
	next := sdktrace.NewSimpleSpanProcessor(exporter)
	p := NewTailSamplingSpanProcessor(next, WithTailPolicy(func(sdktrace.ReadOnlySpan) bool {
		return false // the policy would drop everything
	}))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "marked-by-hand")
	span.SetAttributes(
		attribute.Bool(TailEvaluatedKey, true),
		attribute.Bool(TailKeepKey, true),
	)
	span.End()
	_ = p.ForceFlush(context.Background())

	if len(exporter.GetSpans()) != 1 {
		t.Fatal("an explicit keep was overruled by the policy")
	}
}

func TestTailSamplingSpanProcessor_ForwardsWhenNotEvaluated(t *testing.T) {
	exporter := autoteltesting.NewInMemoryExporter()
	next := sdktrace.NewSimpleSpanProcessor(exporter)
	p := NewTailSamplingSpanProcessor(next)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx := context.Background()

	// Span without tail attributes is always forwarded
	_, span := tracer.Start(ctx, "no-tail-attrs")
	span.End()

	_ = p.ForceFlush(ctx)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
}
