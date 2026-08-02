package processors_test

import (
	"context"
	"testing"

	otelbaggage "go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jagreehal/autotel-go/v2/processors"
)

// tracerWithBaggageProcessor wires the processor in front of a recorder.
func tracerWithBaggageProcessor(t *testing.T, opts ...processors.BaggageSpanProcessorOption) (
	*sdktrace.TracerProvider, *tracetest.SpanRecorder,
) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(processors.NewBaggageSpanProcessor(recorder, opts...)),
	)
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	return provider, recorder
}

// ctxWithBaggage returns a context carrying the given baggage members.
func ctxWithBaggage(t *testing.T, pairs map[string]string) context.Context {
	t.Helper()

	bag := otelbaggage.Baggage{}
	for key, value := range pairs {
		member, err := otelbaggage.NewMember(key, value)
		if err != nil {
			t.Fatalf("new member %q: %v", key, err)
		}
		if bag, err = bag.SetMember(member); err != nil {
			t.Fatalf("set member %q: %v", key, err)
		}
	}
	return otelbaggage.ContextWithBaggage(context.Background(), bag)
}

// spanAttrs returns the recorded span's attributes as a map.
func spanAttrs(t *testing.T, recorder *tracetest.SpanRecorder) map[string]string {
	t.Helper()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(ended))
	}

	out := make(map[string]string)
	for _, kv := range ended[0].Attributes() {
		out[string(kv.Key)] = kv.Value.AsString()
	}
	return out
}

func TestBaggageSpanProcessorCopiesBaggage(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t)
	ctx := ctxWithBaggage(t, map[string]string{"tenant_id": "acme", "region": "eu-west-1"})

	_, span := provider.Tracer("test").Start(ctx, "op")
	span.End()

	attrs := spanAttrs(t, recorder)
	if attrs["baggage.tenant_id"] != "acme" {
		t.Errorf("baggage.tenant_id = %q", attrs["baggage.tenant_id"])
	}
	if attrs["baggage.region"] != "eu-west-1" {
		t.Errorf("baggage.region = %q", attrs["baggage.region"])
	}
}

func TestBaggageSpanProcessorCustomPrefix(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t, processors.WithBaggagePrefix("ctx."))
	ctx := ctxWithBaggage(t, map[string]string{"tenant_id": "acme"})

	_, span := provider.Tracer("test").Start(ctx, "op")
	span.End()

	if got := spanAttrs(t, recorder)["ctx.tenant_id"]; got != "acme" {
		t.Errorf("ctx.tenant_id = %q", got)
	}
}

func TestBaggageSpanProcessorEmptyPrefixCopiesKeysUnchanged(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t, processors.WithBaggagePrefix(""))
	ctx := ctxWithBaggage(t, map[string]string{"tenant_id": "acme"})

	_, span := provider.Tracer("test").Start(ctx, "op")
	span.End()

	if got := spanAttrs(t, recorder)["tenant_id"]; got != "acme" {
		t.Errorf("tenant_id = %q", got)
	}
}

func TestBaggageSpanProcessorAllowlist(t *testing.T) {
	// Baggage arrives from upstream services, so a boundary should copy only the
	// keys it expects rather than everything a caller chose to send.
	provider, recorder := tracerWithBaggageProcessor(t, processors.WithBaggageAllowlist("tenant_id"))
	ctx := ctxWithBaggage(t, map[string]string{
		"tenant_id":  "acme",
		"attacker":   "injected",
		"session_id": "high-cardinality",
	})

	_, span := provider.Tracer("test").Start(ctx, "op")
	span.End()

	attrs := spanAttrs(t, recorder)
	if attrs["baggage.tenant_id"] != "acme" {
		t.Errorf("allowed key missing: %v", attrs)
	}
	for _, blocked := range []string{"baggage.attacker", "baggage.session_id"} {
		if _, present := attrs[blocked]; present {
			t.Errorf("%s should not have been copied", blocked)
		}
	}
}

func TestBaggageSpanProcessorPrefixFilter(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t,
		processors.WithBaggageKeyFilter(processors.BaggagePrefixFilter("app.")),
	)
	ctx := ctxWithBaggage(t, map[string]string{"app.tenant": "acme", "other": "ignored"})

	_, span := provider.Tracer("test").Start(ctx, "op")
	span.End()

	attrs := spanAttrs(t, recorder)
	if attrs["baggage.app.tenant"] != "acme" {
		t.Errorf("prefixed key missing: %v", attrs)
	}
	if _, present := attrs["baggage.other"]; present {
		t.Error("unprefixed key should have been filtered out")
	}
}

func TestBaggageSpanProcessorNoBaggageIsHarmless(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t)

	_, span := provider.Tracer("test").Start(context.Background(), "op")
	span.End()

	if attrs := spanAttrs(t, recorder); len(attrs) != 0 {
		t.Errorf("expected no attributes without baggage, got %v", attrs)
	}
}

func TestBaggageSpanProcessorForwardsLifecycle(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t)

	_, span := provider.Tracer("test").Start(context.Background(), "op")
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
	if len(recorder.Ended()) != 1 {
		t.Error("span was not forwarded to the next processor")
	}
}

// Child spans pick up baggage added after their parent started.
func TestBaggageSpanProcessorAppliesToLaterSpans(t *testing.T) {
	provider, recorder := tracerWithBaggageProcessor(t)
	tracer := provider.Tracer("test")

	ctx, parent := tracer.Start(context.Background(), "parent")
	ctx = ctxWithBaggageOn(t, ctx, "tenant_id", "acme")
	_, child := tracer.Start(ctx, "child")
	child.End()
	parent.End()

	var childAttrs map[string]string
	for _, s := range recorder.Ended() {
		if s.Name() == "child" {
			childAttrs = make(map[string]string)
			for _, kv := range s.Attributes() {
				childAttrs[string(kv.Key)] = kv.Value.AsString()
			}
		}
	}

	if childAttrs["baggage.tenant_id"] != "acme" {
		t.Errorf("child span should carry baggage set after the parent started, got %v", childAttrs)
	}
}

func ctxWithBaggageOn(t *testing.T, ctx context.Context, key, value string) context.Context {
	t.Helper()

	member, err := otelbaggage.NewMember(key, value)
	if err != nil {
		t.Fatalf("new member: %v", err)
	}
	bag, err := otelbaggage.FromContext(ctx).SetMember(member)
	if err != nil {
		t.Fatalf("set member: %v", err)
	}
	return otelbaggage.ContextWithBaggage(ctx, bag)
}
