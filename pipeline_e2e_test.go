package autotel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/processors"
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
