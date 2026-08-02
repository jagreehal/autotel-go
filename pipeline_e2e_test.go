package autotel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/sampling"
)

// These tests drive the real Init path rather than inspecting Config, because
// the two ways this library has shipped dead features were both invisible to
// config-level assertions: an option set its field correctly and the value was
// then discarded downstream.

// initWithExporter runs Init with an in-memory exporter and returns the spans it
// received after cleanup.
func initWithExporter(t *testing.T, extra ...autotel.Option) *tracetest.InMemoryExporter {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	opts := append([]autotel.Option{
		autotel.WithService("e2e"),
		autotel.WithDebug(false),
		autotel.WithMetrics(false),
		autotel.WithSampler(sdktrace.AlwaysSample()),
		autotel.WithSpanExporters(exporter),
		autotel.WithBatchTimeout(50 * time.Millisecond),
	}, extra...)

	cleanup, err := autotel.Init(context.Background(), opts...)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(cleanup)

	return exporter
}

func spanNames(exporter *tracetest.InMemoryExporter) []string {
	var names []string
	for _, s := range exporter.GetSpans() {
		names = append(names, s.Name)
	}
	return names
}

// setBaggage attaches the given members to ctx.
func setBaggage(ctx context.Context, pairs map[string]string) (context.Context, error) {
	bag := otelbaggage.Baggage{}
	for key, value := range pairs {
		member, err := otelbaggage.NewMember(key, value)
		if err != nil {
			return ctx, err
		}
		if bag, err = bag.SetMember(member); err != nil {
			return ctx, err
		}
	}
	return otelbaggage.ContextWithBaggage(ctx, bag), nil
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestWithSpanFilterDropsSpansEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithSpanFilter(func(s sdktrace.ReadOnlySpan) bool {
		return s.Name() != "healthz"
	}))

	for _, name := range []string{"healthz", "checkout"} {
		_, span := autotel.Start(context.Background(), name)
		span.End()
	}

	// Force the batch processor to flush before asserting.
	time.Sleep(150 * time.Millisecond)

	names := spanNames(exporter)
	if contains(names, "healthz") {
		t.Errorf("filtered span was exported anyway: %v", names)
	}
	if !contains(names, "checkout") {
		t.Errorf("unfiltered span was not exported: %v", names)
	}
}

func TestWithTailSamplingDropsSpansEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithTailSampling(true))

	_, dropped := autotel.Start(context.Background(), "dropped")
	dropped.SetAttribute(processors.TailEvaluatedKey, true)
	dropped.SetAttribute(processors.TailKeepKey, false)
	dropped.End()

	_, kept := autotel.Start(context.Background(), "kept")
	kept.SetAttribute(processors.TailEvaluatedKey, true)
	kept.SetAttribute(processors.TailKeepKey, true)
	kept.End()

	_, unevaluated := autotel.Start(context.Background(), "unevaluated")
	unevaluated.End()

	time.Sleep(150 * time.Millisecond)

	names := spanNames(exporter)
	if contains(names, "dropped") {
		t.Errorf("tail-dropped span was exported anyway: %v", names)
	}
	for _, want := range []string{"kept", "unevaluated"} {
		if !contains(names, want) {
			t.Errorf("span %q should have been kept: %v", want, names)
		}
	}
}

// The whole point of an error rate: a failure survives a baseline that keeps
// nothing. This shipped broken because AdaptiveSampler stored the rate and never
// read it, and the only test asserted the sampler returned a non-nil result.
func TestAdaptiveSamplerKeepsErrorsBelowBaselineEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0),
		sampling.WithErrorRate(1.0),
	))

	for i := 0; i < 20; i++ {
		_, routine := autotel.Start(context.Background(), "routine")
		routine.End()
	}

	_, failed := autotel.Start(context.Background(), "failed")
	failed.SetStatus(codes.Error, "payment declined")
	failed.End()

	time.Sleep(150 * time.Millisecond)

	names := spanNames(exporter)
	if !contains(names, "failed") {
		t.Errorf("the error span was dropped despite WithErrorRate(1.0): %v", names)
	}
	if contains(names, "routine") {
		t.Errorf("a zero baseline still exported routine spans: %v", names)
	}
}

// A kept error is only useful with the spans it hangs off. Keeping the failed
// span alone produces a waterfall with the middle missing, which is what an
// OTLP receiver renders as a root span with a parent that does not exist.
func TestKeptErrorBringsItsAncestorsEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0), // the parent would be dropped on its own
		sampling.WithErrorRate(1.0),
	))

	ctx, parent := autotel.Start(context.Background(), "checkout")
	_, child := autotel.Start(ctx, "charge-card")
	child.SetStatus(codes.Error, "card declined")
	child.End()
	parent.End()

	time.Sleep(150 * time.Millisecond)

	names := spanNames(exporter)
	for _, want := range []string{"charge-card", "checkout"} {
		if !contains(names, want) {
			t.Errorf("span %q missing; a kept error must bring its trace: %v", want, names)
		}
	}
}

// Stickiness must not become "keep everything": a trace that never failed is
// still subject to the baseline.
func TestUnrelatedTracesAreNotKeptBySomeoneElsesError(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0),
		sampling.WithErrorRate(1.0),
	))

	_, failed := autotel.Start(context.Background(), "failed")
	failed.SetStatus(codes.Error, "boom")
	failed.End()

	for i := 0; i < 20; i++ {
		_, ok := autotel.Start(context.Background(), "healthy")
		ok.End()
	}

	time.Sleep(150 * time.Millisecond)

	if names := spanNames(exporter); contains(names, "healthy") {
		t.Errorf("an unrelated trace was kept by another trace's error: %v", names)
	}
}

// Slow spans are the other decision a head sampler cannot make, since duration
// does not exist until the span ends.
func TestAdaptiveSamplerKeepsSlowSpansEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0),
		sampling.WithErrorRate(0),
		sampling.WithSlowThreshold(int64(20*time.Millisecond)),
		sampling.WithSlowRate(1.0),
	))

	_, quick := autotel.Start(context.Background(), "quick")
	quick.End()

	_, slow := autotel.Start(context.Background(), "slow")
	time.Sleep(30 * time.Millisecond)
	slow.End()

	time.Sleep(150 * time.Millisecond)

	names := spanNames(exporter)
	if !contains(names, "slow") {
		t.Errorf("the slow span was dropped despite WithSlowRate(1.0): %v", names)
	}
	if contains(names, "quick") {
		t.Errorf("a fast span was exported under a zero baseline: %v", names)
	}
}

func TestWithBaggageAttributesEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithBaggageAttributes(
		processors.WithBaggageAllowlist("tenant_id"),
	))

	ctx, err := setBaggage(context.Background(), map[string]string{
		"tenant_id": "acme",
		"secret":    "not-copied",
	})
	if err != nil {
		t.Fatalf("baggage: %v", err)
	}

	_, span := autotel.Start(ctx, "op")
	span.End()

	time.Sleep(150 * time.Millisecond)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans exported")
	}

	attrs := make(map[string]string)
	for _, kv := range spans[0].Attributes {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	if attrs["baggage.tenant_id"] != "acme" {
		t.Errorf("baggage was not copied onto the span: %v", attrs)
	}
	if _, present := attrs["baggage.secret"]; present {
		t.Error("a key outside the allowlist was copied onto the span")
	}
}

// WithSpanProcessors must reach the provider alongside the built-in pipeline.
func TestWithSpanProcessorsEndToEnd(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	_ = initWithExporter(t, autotel.WithSpanProcessors(recorder))

	_, span := autotel.Start(context.Background(), "custom-processor")
	span.End()

	if len(recorder.Ended()) == 0 {
		t.Error("a custom span processor received nothing")
	}
}

// WithPIIRedaction is advertised as the replacement for the removed attribute
// redactor, so it has to actually redact through the real Init path.
func TestWithPIIRedactionEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithPIIRedaction())

	_, span := autotel.Start(context.Background(), "op")
	span.SetAttribute("email", "alice@example.com")
	span.End()

	time.Sleep(150 * time.Millisecond)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans exported")
	}

	var found bool
	for _, kv := range spans[0].Attributes {
		if string(kv.Key) != "email" {
			continue
		}
		found = true
		if kv.Value.Type() == attribute.STRING && kv.Value.AsString() == "alice@example.com" {
			t.Error("PII redaction did not redact: the raw email reached the exporter")
		}
	}
	// Guards against passing vacuously because the attribute was never recorded.
	if !found {
		t.Fatal("the email attribute never reached the exporter, so this proves nothing")
	}
}
