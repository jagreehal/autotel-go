package correlationid

import (
	"context"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func TestGenerateCorrelationID(t *testing.T) {
	id := GenerateCorrelationID()
	if len(id) != 16 {
		t.Errorf("expected 16 hex chars, got %d", len(id))
	}
	matched, _ := regexp.MatchString("^[0-9a-f]{16}$", id)
	if !matched {
		t.Errorf("expected hex string, got %q", id)
	}
	// Uniqueness (probabilistic)
	id2 := GenerateCorrelationID()
	if id == id2 {
		t.Error("expected different IDs")
	}
}

func TestGetOrCreateCorrelationID(t *testing.T) {
	ctx := context.Background()
	id := GetOrCreateCorrelationID(ctx)
	if id == "" || len(id) != 16 {
		t.Errorf("expected 16-char id, got %q", id)
	}
	ctx = context.WithValue(ctx, correlationIDKey, "existing-id")
	if got := GetOrCreateCorrelationID(ctx); got != "existing-id" {
		t.Errorf("expected existing-id, got %q", got)
	}
}

func TestSetCorrelationIDInBaggage(t *testing.T) {
	ctx := context.Background()
	ctx, err := SetCorrelationIDInBaggage(ctx, "baggage-only-id")
	if err != nil {
		t.Fatal(err)
	}
	// Context value not set, so GetCorrelationID should still get from baggage
	if got := GetCorrelationID(ctx); got != "baggage-only-id" {
		t.Errorf("expected baggage-only-id, got %q", got)
	}
}

func TestGetCorrelationID_FromContext(t *testing.T) {
	ctx := context.Background()
	if got := GetCorrelationID(ctx); got != "" {
		t.Errorf("empty context: expected \"\", got %q", got)
	}

	ctx = context.WithValue(ctx, correlationIDKey, "a1b2c3d4e5f67890")
	if got := GetCorrelationID(ctx); got != "a1b2c3d4e5f67890" {
		t.Errorf("from context: expected a1b2c3d4e5f67890, got %q", got)
	}
}

func TestGetCorrelationID_FromBaggage(t *testing.T) {
	ctx := context.Background()
	member, err := baggage.NewMember(CORRELATION_ID_BAGGAGE_KEY, "baggage-id-12345678")
	if err != nil {
		t.Fatal(err)
	}
	bag, _ := baggage.New(member)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	if got := GetCorrelationID(ctx); got != "baggage-id-12345678" {
		t.Errorf("from baggage: expected baggage-id-12345678, got %q", got)
	}
}

func TestGetCorrelationID_ContextOverBaggage(t *testing.T) {
	ctx := context.WithValue(context.Background(), correlationIDKey, "ctx-first")
	member, _ := baggage.NewMember(CORRELATION_ID_BAGGAGE_KEY, "baggage-second")
	bag, _ := baggage.New(member)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	if got := GetCorrelationID(ctx); got != "ctx-first" {
		t.Errorf("context should override baggage: got %q", got)
	}
}

func TestSetCorrelationID(t *testing.T) {
	ctx := context.Background()
	ctx, err := SetCorrelationID(ctx, "custom-id-1234567", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := GetCorrelationID(ctx); got != "custom-id-1234567" {
		t.Errorf("expected custom-id-1234567, got %q", got)
	}

	ctx, err = SetCorrelationID(ctx, "with-baggage", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := GetCorrelationID(ctx); got != "with-baggage" {
		t.Errorf("expected with-baggage, got %q", got)
	}
	bag := baggage.FromContext(ctx)
	if v := bag.Member(CORRELATION_ID_BAGGAGE_KEY).Value(); v != "with-baggage" {
		t.Errorf("baggage: expected with-baggage, got %q", v)
	}
}

func TestRunWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	got, err := RunWithCorrelationID(ctx, "run-id-12345678", false, func(ctx context.Context) (string, error) {
		return GetCorrelationID(ctx), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "run-id-12345678" {
		t.Errorf("expected run-id-12345678, got %q", got)
	}
	// Outside fn, original ctx unchanged
	if GetCorrelationID(ctx) != "" {
		t.Error("original context should not have correlation ID")
	}
}

func TestGetCorrelationID_FallbackTraceID(t *testing.T) {
	// When no context value and no baggage, GetCorrelationID can fall back to trace ID.
	// We need a context with a valid span to test that. Create a noop span context.
	tid := trace.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	sid := trace.SpanID([8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	got := GetCorrelationID(ctx)
	// Trace ID as hex is 32 chars; we take first 16
	if len(got) != 16 {
		t.Errorf("fallback trace ID prefix: expected 16 chars, got %d %q", len(got), got)
	}
}
