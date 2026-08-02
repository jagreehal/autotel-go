package autotel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/circuitbreaker"
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

// A configured rate limit has to bound every span, not just the ones created
// through autotel.Start. When the check lived in Start, a service using
// middleware, messaging or any third-party instrumentation had a limit that
// bounded almost nothing.
func TestRateLimitCoversSpansFromAnySourceEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithRateLimit(3, 3))

	// Deliberately not autotel.Start: this is how otelhttp, messaging and every
	// contrib instrumentation package creates spans.
	tracer := otel.Tracer("third-party-instrumentation")
	for i := 0; i < 20; i++ {
		_, span := tracer.Start(context.Background(), "external")
		span.End()
	}

	time.Sleep(150 * time.Millisecond)

	got := len(exporter.GetSpans())
	if got > 3 {
		t.Errorf("a limit of 3/sec exported %d spans created outside autotel.Start", got)
	}
	if got == 0 {
		t.Error("the limit dropped everything; the burst of 3 should have passed")
	}
}

// Shedding load must not shred the traces it keeps. A span whose parent is
// already sampled passes the guard, so a kept trace arrives whole.
func TestRateLimitDoesNotSplitAKeptTraceEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithRateLimit(1, 1))

	ctx, parent := autotel.Start(context.Background(), "root")
	for i := 0; i < 5; i++ {
		_, child := autotel.Start(ctx, "child")
		child.End()
	}
	parent.End()

	time.Sleep(150 * time.Millisecond)

	if got := len(exporter.GetSpans()); got != 6 {
		t.Errorf("kept trace arrived with %d of its 6 spans: %v", got, spanNames(exporter))
	}
}

// An open circuit breaker stops spans from every source too.
func TestCircuitBreakerCoversSpansFromAnySourceEndToEnd(t *testing.T) {
	// A closed breaker configured the normal way must not block anything.
	open := initWithExporter(t, autotel.WithCircuitBreaker(5, 1, time.Minute))
	_, allowed := autotel.Start(context.Background(), "allowed")
	allowed.End()
	time.Sleep(150 * time.Millisecond)
	if len(open.GetSpans()) == 0 {
		t.Error("a closed breaker dropped spans")
	}

	// Tripping it needs a breaker this test holds, so it is built directly.
	breaker := circuitbreaker.NewCircuitBreaker(1, 1, time.Minute)
	breaker.RecordFailure()

	exporter := initWithExporter(t, func(c *autotel.Config) { c.CircuitBreaker = breaker })

	tracer := otel.Tracer("third-party-instrumentation")
	_, span := tracer.Start(context.Background(), "external")
	span.End()

	time.Sleep(150 * time.Millisecond)

	if got := len(exporter.GetSpans()); got != 0 {
		t.Errorf("an open breaker exported %d spans", got)
	}
}

// A consumer span linked to a sampled producer should survive, so an
// event-driven trace does not lose its second half.
func TestWithLinksBasedSamplingKeepsLinkedSpansEndToEnd(t *testing.T) {
	exporter := initWithExporter(t, autotel.WithLinksBasedSampling(1.0))

	sampled := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{9},
		SpanID:     oteltrace.SpanID{9},
		TraceFlags: oteltrace.FlagsSampled,
	})

	_, consumer := autotel.Start(context.Background(), "consumer",
		oteltrace.WithLinks(oteltrace.Link{SpanContext: sampled}))
	consumer.End()

	time.Sleep(150 * time.Millisecond)

	if names := spanNames(exporter); !contains(names, "consumer") {
		t.Errorf("a span linked to a sampled producer was dropped: %v", names)
	}
}

// The budget has to bind once the sampler has measured a window of traffic.
func TestWithTargetRateSamplerBindsAfterMeasuringEndToEnd(t *testing.T) {
	now := time.Unix(0, 0)
	exporter := initWithExporter(t, autotel.WithTargetRateSampler(
		sampling.WithTargetSpansPerSecond(1),
		sampling.WithAdjustInterval(time.Minute),
		sampling.WithTargetRateClock(func() time.Time { return now }),
	))

	// Nothing measured yet, so the first window keeps everything.
	for i := 0; i < 200; i++ {
		_, span := autotel.Start(context.Background(), "first")
		span.End()
	}
	now = now.Add(time.Minute)
	for i := 0; i < 200; i++ {
		_, span := autotel.Start(context.Background(), "second")
		span.End()
	}
	time.Sleep(200 * time.Millisecond)

	var first, second int
	for _, name := range spanNames(exporter) {
		if name == "first" {
			first++
		} else if name == "second" {
			second++
		}
	}
	if first != 200 {
		t.Errorf("first window kept %d/200; with nothing measured it keeps all", first)
	}
	// 200 a minute is 3.3/s against a budget of 1/s, so roughly a third survives.
	if second == 0 || second >= 200 {
		t.Errorf("second window kept %d/200; the budget should bind without emptying", second)
	}
}

// Identity reaches the exporter as resource attributes, or a backend cannot tell
// two deployments of the same service apart.
func TestServiceVersionAndEnvironmentReachTheExporterEndToEnd(t *testing.T) {
	exporter := initWithExporter(t,
		autotel.WithServiceVersion("4.5.6"),
		autotel.WithEnvironment("staging"),
	)

	_, span := autotel.Start(context.Background(), "identified")
	span.End()
	time.Sleep(150 * time.Millisecond)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans exported")
	}
	got := map[string]string{}
	for _, attr := range spans[0].Resource.Attributes() {
		got[string(attr.Key)] = attr.Value.Emit()
	}
	if got["service.version"] != "4.5.6" {
		t.Errorf("service.version = %q, want 4.5.6", got["service.version"])
	}
	if got["deployment.environment"] != "staging" {
		t.Errorf("deployment.environment = %q, want staging", got["deployment.environment"])
	}
}
