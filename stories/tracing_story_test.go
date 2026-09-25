// Package stories holds the executable-stories showcase for autotel-go.
//
// It exercises autotel's public API and publishes the run as living
// documentation, with the spans the run actually recorded attached to each
// scenario.
//
// Every claim is wrapped in s.Expect so the report can show it was checked.
// A bare s.Then followed by t.Fatalf reads as prose to the report: the check
// runs, but nothing records that it happened.
package stories

import (
	"context"
	"errors"
	"testing"

	es "github.com/jagreehal/executable-stories/packages/executable-stories-go"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestMain(m *testing.M) { es.RunAndReport(m) }

func init() {
	es.Feature(es.FeatureSpec{
		Kind:  "ability",
		Title: "Trace a Go function without writing tracing code",
		Narrative: "Instrumenting by hand means a span to start, a status to set, an " +
			"error to record and a context to thread through every call — repeated at " +
			"every function worth watching, and wrong in a different way each time. " +
			"autotel takes the function and returns what it always returned.",
		Tags: []string{"otel", "tracing"},
		Glossary: []es.RawGlossaryTerm{
			{Term: "span", Definition: "One timed operation, with a name, a status and attributes."},
			{Term: "trace", Definition: "The spans from one request, linked parent to child."},
			{Term: "trace context", Definition: "What a span carries in the Go context so the next call becomes its child."},
		},
	})
}

// tracedByAutotel is the product code these scenarios exercise.
var tracedByAutotel = es.WithCovers("functional.go")

// attachTrace hands the report the spans this scenario recorded, along with
// the trace they belong to. The trace only exists once the code has run, so
// the init-time bridge cannot see it — this is where the trace link is wired.
// Set OTEL_TRACE_URL_TEMPLATE to turn it into a link to your backend.
func attachTrace(s *es.S, spans []sdktrace.ReadOnlySpan) {
	ref := es.TraceRef{}
	for _, span := range spans {
		if !span.Parent().IsValid() {
			ref.TraceID = span.SpanContext().TraceID().String()
			ref.SpanID = span.SpanContext().SpanID().String()
			break
		}
	}
	s.AttachSpansWithTrace(serializeSpans(spans), ref)
}

// serializeSpans converts recorded spans into the shape the story report
// renders as a trace waterfall.
func serializeSpans(spans []sdktrace.ReadOnlySpan) []any {
	out := make([]any, 0, len(spans))
	for _, span := range spans {
		entry := map[string]any{
			"spanId":      span.SpanContext().SpanID().String(),
			"name":        span.Name(),
			"startTimeMs": float64(span.StartTime().UnixMilli()),
			"durationMs":  float64(span.EndTime().Sub(span.StartTime()).Microseconds()) / 1000,
			"status":      statusOf(span),
		}
		if parent := span.Parent(); parent.IsValid() {
			entry["parentSpanId"] = parent.SpanID().String()
		}
		if msg := span.Status().Description; msg != "" {
			entry["statusMessage"] = msg
		}
		out = append(out, entry)
	}
	return out
}

func statusOf(span sdktrace.ReadOnlySpan) string {
	switch span.Status().Code {
	case codes.Error:
		return "error"
	case codes.Ok:
		return "ok"
	default:
		return "unset"
	}
}

// spanNamed finds a recorded span by name.
func spanNamed(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	return nil
}

// attrInt reads an int attribute off a recorded span.
func attrInt(span sdktrace.ReadOnlySpan, key string) (int64, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsInt64(), true
		}
	}
	return 0, false
}

func chargeCard(ctx context.Context, amount int) any {
	return autotel.TraceFunc(ctx, "payment.charge", func(tc autotel.TraceContext) (int, error) {
		tc.SetAttribute("payment.amount", amount)
		if amount <= 0 {
			return 0, errors.New("invalid amount")
		}
		return amount, nil
	})
}

func TestTracedFunctionRecordsASpan(t *testing.T) {
	s := es.Init(t, "A traced function records a span named after the operation",
		es.WithTags("otel", "tracing"), tracedByAutotel)
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	s.Given("a payment function wrapped once with autotel TraceFunc")
	s.Code("The whole instrumentation", `autotel.TraceFunc(ctx, "payment.charge", func(tc autotel.TraceContext) (int, error) {
	tc.SetAttribute("payment.amount", amount)
	return amount, nil
})`, "go")

	s.When("the function is called")
	result := chargeCard(context.Background(), 2500)

	s.Expect("it returns its ordinary value, unchanged by instrumentation", func() {
		s.Kv("captured", result)
		s.Check(result == 2500, "expected the untouched return value 2500, got %v", result)
	})

	spans := exporter.GetSpans()
	attachTrace(s, spans)

	s.Expect("a span was recorded without a single line of tracing code", func() {
		rows := make([][]string, 0, len(spans))
		for _, span := range spans {
			rows = append(rows, []string{span.Name(), statusOf(span)})
		}
		s.Table("Spans recorded by this scenario", []string{"name", "status"}, rows)

		span := spanNamed(spans, "payment.charge")
		if !s.Check(span != nil, "expected a span named payment.charge, got %d spans", len(spans)) {
			return
		}
		s.Check(statusOf(span) != "error", "expected a successful charge to leave the span unfailed")
	})

	s.Expect("the attribute the function set is on the span", func() {
		span := spanNamed(spans, "payment.charge")
		if !s.Check(span != nil, "expected a span named payment.charge") {
			return
		}
		amount, ok := attrInt(span, "payment.amount")
		if !s.Check(ok, "expected payment.amount on the span") {
			return
		}
		s.Kv("payment.amount", amount)
		s.Check(amount == 2500, "expected payment.amount 2500, got %d", amount)
	})
}

func TestTracedFunctionRecordsFailure(t *testing.T) {
	s := es.Init(t, "A traced function that fails records the failure on its span",
		es.WithTags("otel", "tracing"), tracedByAutotel)
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	s.Given("the same payment function, with no error handling added")

	s.When("it is called with an amount it rejects")
	returned := chargeCard(context.Background(), -1)

	spans := exporter.GetSpans()
	attachTrace(s, spans)

	s.Expect("the span is marked as failed", func() {
		span := spanNamed(spans, "payment.charge")
		if !s.Check(span != nil, "expected a span named payment.charge, got %d spans", len(spans)) {
			return
		}
		s.Kv("status", statusOf(span))
		s.Check(statusOf(span) == "error", "expected error status, got %q", statusOf(span))
	})

	s.Expect("the caller still sees the error it would have seen without autotel", func() {
		s.Note("autotel records the exception on the span and returns the error. It never swallows a failure.")
		err, ok := returned.(error)
		if !s.Check(ok, "expected the error returned to the caller, got %#v", returned) {
			return
		}
		s.Kv("returned to caller", err.Error())
		s.Check(err.Error() == "invalid amount", "expected the function's own error, got %q", err.Error())
	})
}

func TestNestedTracedCallsJoinOneTrace(t *testing.T) {
	s := es.Init(t, "Nested traced calls join a single trace",
		es.WithTags("otel", "tracing"), tracedByAutotel)
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	s.Given("a checkout function that calls the payment function")

	s.When("checkout runs")
	autotel.TraceFunc(context.Background(), "checkout", func(tc autotel.TraceContext) (int, error) {
		chargeCard(tc.Context(), 4200)
		return 4200, nil
	})

	spans := exporter.GetSpans()
	attachTrace(s, spans)

	outer := spanNamed(spans, "checkout")
	inner := spanNamed(spans, "payment.charge")

	s.Expect("the inner span is a child of the outer one", func() {
		s.Mermaid("graph TD\n  C[\"checkout\"] --> P[\"payment.charge\"]", "Trace shape recorded by this run")

		if !s.Check(outer != nil && inner != nil, "expected both spans, got outer=%v inner=%v", outer != nil, inner != nil) {
			return
		}
		s.Check(inner.Parent().SpanID() == outer.SpanContext().SpanID(),
			"expected the inner span parented to the outer span")
	})

	s.Expect("both spans share one trace id", func() {
		if !s.Check(outer != nil && inner != nil, "expected both spans") {
			return
		}
		s.Kv("trace id", outer.SpanContext().TraceID().String())
		s.Check(inner.SpanContext().TraceID() == outer.SpanContext().TraceID(),
			"expected one trace across both spans")
	})

	s.And("the caller wrote no context propagation code at all")
	s.Note("Parenting comes from the context TraceFunc hands to the body.")
}
