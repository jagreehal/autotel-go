package autotel

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The load-bearing property: nothing is deployed to turn this on. A sampler
// that drops everything still keeps the request that asked to be kept.
func TestDebugBaggageKeepsARequestTheSamplerDrops(t *testing.T) {
	names := debugRun(t, WithDebugCapture())
	if len(names) != 1 || names[0] != "debugged" {
		t.Fatalf("exported %v, want only the debugged request", names)
	}
}

// Callers set baggage, so the key does nothing until a service opts in.
func TestDebugBaggageIsIgnoredWithoutTheOption(t *testing.T) {
	if names := debugRun(t); len(names) != 0 {
		t.Fatalf("exported %v, want nothing", names)
	}
}

// debugRun starts three spans under a sampler that drops everything, one of
// them carrying the debug key, and returns the names that were exported.
func debugRun(t *testing.T, extra ...Option) []string {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	cleanup, err := Init(context.Background(), append([]Option{
		WithService("debug-capture"),
		WithDebug(false),
		WithMetrics(false),
		WithLogs(false),
		WithSampler(sdktrace.NeverSample()),
		WithTailSampling(true),
		WithSpanExporters(exporter),
		WithBatchTimeout(10 * time.Millisecond),
	}, extra...)...)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer cleanup()

	_, plain := Start(context.Background(), "plain")
	plain.End()

	ctx, err := SetBaggage(context.Background(), DebugBaggageKey, "ticket-4411")
	if err != nil {
		t.Fatal(err)
	}
	_, debugged := Start(ctx, "debugged")
	debugged.End()

	off, _ := SetBaggage(context.Background(), DebugBaggageKey, "0")
	_, switchedOff := Start(off, "switched-off")
	switchedOff.End()

	// Flush, not Shutdown: the in-memory exporter forgets its spans when shut down.
	if err := Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, span := range exporter.GetSpans() {
		names = append(names, span.Name)
	}

	return names
}

func TestSetBaggageRejectsAnInvalidKey(t *testing.T) {
	ctx := context.Background()
	if got, err := SetBaggage(ctx, "", "v"); err == nil || got != ctx {
		t.Fatalf("err %v; want an error and the context unchanged", err)
	}
}
