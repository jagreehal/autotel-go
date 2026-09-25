package autotel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/processors"
)

// DebugBaggageKey is the baggage key that turns on full-fidelity capture for
// one request. Any value other than "" or "0" turns it on.
//
// Baggage arrives on the request and propagates, so a gateway, a proxy, a
// feature flag or a curl can turn this on for one user and it follows them
// across every service. Nobody deploys anything to debug a live problem.
//
//	ctx, _ = autotel.SetBaggage(ctx, autotel.DebugBaggageKey, "ticket-4411")
//
// With WithDebugCapture enabled, spans started under that context are kept
// whatever the sampler or the tail policy would have decided.
const DebugBaggageKey = "autotel.debug"

// SetBaggage returns ctx carrying key=value as W3C baggage, alongside any
// baggage it already had.
//
// An attribute stays on one span. Baggage travels: it is copied into the
// `baggage` header of every outgoing request the propagator writes, and
// WithBaggageAttributes copies it onto every span started under ctx, in this
// service and the ones downstream. Keep it to a few short, non-secret values,
// because it goes wherever the request goes.
func SetBaggage(ctx context.Context, key, value string) (context.Context, error) {
	member, err := otelbaggage.NewMemberRaw(key, value)
	if err != nil {
		return ctx, fmt.Errorf("baggage %q: %w", key, err)
	}

	bag, err := otelbaggage.FromContext(ctx).SetMember(member)
	if err != nil {
		return ctx, fmt.Errorf("baggage %q: %w", key, err)
	}

	return otelbaggage.ContextWithBaggage(ctx, bag), nil
}

// debugCaptureRequested reports whether the request in ctx asked for full
// fidelity through DebugBaggageKey.
func debugCaptureRequested(ctx context.Context) bool {
	value := otelbaggage.FromContext(ctx).Member(DebugBaggageKey).Value()

	return value != "" && value != "0"
}

// debugCaptureSampler keeps every span of a request carrying DebugBaggageKey
// and defers to next for everything else.
//
// It sits inside the rate limiter and circuit breaker, so a caller setting
// the key cannot exceed their budget. The span is also marked as evaluated
// and kept, so a tail policy downstream keeps it too.
type debugCaptureSampler struct {
	next trace.Sampler
}

func (s debugCaptureSampler) ShouldSample(p trace.SamplingParameters) trace.SamplingResult {
	if !debugCaptureRequested(p.ParentContext) {
		return s.next.ShouldSample(p)
	}

	return trace.SamplingResult{
		Decision: trace.RecordAndSample,
		Attributes: []attribute.KeyValue{
			attribute.Bool(processors.TailEvaluatedKey, true),
			attribute.Bool(processors.TailKeepKey, true),
		},
		Tracestate: oteltrace.SpanContextFromContext(p.ParentContext).TraceState(),
	}
}

func (s debugCaptureSampler) Description() string {
	return "debugCapture(" + s.next.Description() + ")"
}
