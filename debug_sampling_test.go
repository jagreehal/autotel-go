package autotel_test

import (
	"context"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
)

type countingExporter struct{ spans atomic.Int64 }

func (e *countingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.spans.Add(int64(len(spans)))
	return nil
}

func (e *countingExporter) Shutdown(context.Context) error { return nil }

func exportedOf(t *testing.T, total int, opts ...autotel.Option) int64 {
	t.Helper()
	exporter := &countingExporter{}
	opts = append(opts, autotel.WithService("debug-sampling"), autotel.WithSpanExporters(exporter))
	cleanup, err := autotel.Init(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	tracer := otel.Tracer("debug-sampling")
	for range total {
		_, span := tracer.Start(context.Background(), "routine")
		span.End()
	}
	cleanup()
	return exporter.spans.Load()
}

// Debug mode announces AlwaysSample, so the default adaptive sampler's tail
// policy must not go on to drop the routine spans a developer is looking for.
func TestDebugModeKeepsEverySpan(t *testing.T) {
	const total = 200
	if got := exportedOf(t, total, autotel.WithDebug(true)); got != total {
		t.Fatalf("debug mode exported %d of %d spans", got, total)
	}
}

func TestBaselineStillAppliesOutsideDebug(t *testing.T) {
	const total = 200
	if got := exportedOf(t, total, autotel.WithDebug(false), autotel.WithAdaptiveSampler()); got >= total {
		t.Fatalf("exported all %d spans; the baseline rate was not applied", got)
	}
}
