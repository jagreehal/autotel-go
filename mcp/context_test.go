package mcp

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
)

func TestExtractContextFromMeta_Empty(t *testing.T) {
	ctx := context.Background()
	out := ExtractContextFromMeta(ctx, nil)
	if out != ctx {
		t.Error("expected same context when meta is nil")
	}
	out = ExtractContextFromMeta(ctx, map[string]any{})
	if out != ctx {
		t.Error("expected same context when meta is empty")
	}
}

func TestExtractContextFromMeta_WithTraceparent(t *testing.T) {
	ctx := context.Background()
	meta := map[string]any{
		MetaKeyTraceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	out := ExtractContextFromMeta(ctx, meta)
	if out == ctx {
		t.Error("expected new context when traceparent present")
	}
}

func TestInjectContextToMeta(t *testing.T) {
	ctx := context.Background()
	meta := InjectContextToMeta(ctx)
	if meta == nil {
		t.Fatal("expected non-nil map")
	}
	// With no active span, inject may still produce empty or non-empty carrier depending on propagator
	_ = meta
}

func TestInjectExtractRoundtrip(t *testing.T) {
	// Create a context with trace context (simulate a span)
	carrier := make(map[string]string)
	carrier["traceparent"] = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := context.Background()
	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	ctx = prop.Extract(ctx, propagation.MapCarrier(carrier))

	meta := InjectContextToMeta(ctx)
	if meta[MetaKeyTraceparent] != carrier["traceparent"] {
		t.Errorf("inject: got %v", meta[MetaKeyTraceparent])
	}

	metaAny := make(map[string]any)
	for k, v := range meta {
		metaAny[k] = v
	}
	ctx2 := ExtractContextFromMeta(context.Background(), metaAny)
	meta2 := InjectContextToMeta(ctx2)
	if meta2[MetaKeyTraceparent] != carrier["traceparent"] {
		t.Errorf("after extract: got %v", meta2[MetaKeyTraceparent])
	}
}

func TestMergeMeta(t *testing.T) {
	base := map[string]any{"custom": "value"}
	injected := map[string]any{MetaKeyTraceparent: "00-abc-123-01"}
	merged := MergeMeta(base, injected)
	if merged["custom"] != "value" {
		t.Errorf("expected custom preserved, got %v", merged["custom"])
	}
	if merged[MetaKeyTraceparent] != "00-abc-123-01" {
		t.Errorf("expected traceparent, got %v", merged[MetaKeyTraceparent])
	}
	// Base unchanged
	if base[MetaKeyTraceparent] != nil {
		t.Error("base should not be modified")
	}
}
