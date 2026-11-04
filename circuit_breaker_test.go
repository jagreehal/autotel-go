package autotel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// flakyExporter fails until healthy is set, and counts spans it took.
type flakyExporter struct {
	healthy  atomic.Bool
	exported atomic.Int64
}

func (e *flakyExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if !e.healthy.Load() {
		return errors.New("backend down")
	}
	e.exported.Add(int64(len(spans)))
	return nil
}

func (e *flakyExporter) Shutdown(context.Context) error { return nil }

func sendSpan(ctx context.Context) {
	_, span := otel.Tracer("t").Start(ctx, "work")
	span.End()
}

func TestCircuitBreakerOpensOnFailedExportsAndRecovers(t *testing.T) {
	exporter := &flakyExporter{}
	// Debug stays on: a healthy console exporter must not hide a failing backend.
	_, err := Init(context.Background(), WithDebug(true), WithService("x"),
		WithSpanExporters(exporter), WithCircuitBreaker(2, 1, 100*time.Millisecond),
		func(c *Config) { c.BatchTimeout = 5 * time.Millisecond })
	require.NoError(t, err)
	t.Cleanup(DisableDebug)
	ctx := context.Background()

	dropped := func() bool {
		_, span := otel.Tracer("t").Start(ctx, "probe")
		defer span.End()
		return !span.IsRecording()
	}

	require.Eventually(t, func() bool {
		sendSpan(ctx)
		return dropped()
	}, 2*time.Second, 5*time.Millisecond, "failed exports should open the breaker")

	exporter.healthy.Store(true)
	require.Eventually(t, func() bool {
		sendSpan(ctx)
		return !dropped() && exporter.exported.Load() > 0
	}, 2*time.Second, 10*time.Millisecond, "a healthy backend should close it again")

	require.NoError(t, Shutdown(ctx))
	assert.Positive(t, exporter.exported.Load())
}

func TestIsTracingEnabledExportsNothing(t *testing.T) {
	exporter := &keepingExporter{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithSpanExporters(exporter))
	require.NoError(t, err)
	assert.True(t, IsTracingEnabled(context.Background()))
	require.NoError(t, Shutdown(context.Background()))
	assert.Empty(t, exporter.spans)
}

// The batch processor drops export errors, so a traces-only setup such as Jaeger
// would otherwise report success while losing everything.
func TestShutdownReportsFailedTraceExports(t *testing.T) {
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithSpanExporters(&flakyExporter{}))
	require.NoError(t, err)
	sendSpan(context.Background())
	assert.ErrorContains(t, Flush(context.Background()), "backend down")
	assert.ErrorContains(t, Shutdown(context.Background()), "backend down")
}

func TestShutdownForgetsAFailureThatRecovered(t *testing.T) {
	exporter := &flakyExporter{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithSpanExporters(exporter),
		func(c *Config) { c.BatchTimeout = 5 * time.Millisecond })
	require.NoError(t, err)
	sendSpan(context.Background())
	time.Sleep(50 * time.Millisecond)
	exporter.healthy.Store(true)
	sendSpan(context.Background())
	require.NoError(t, Shutdown(context.Background()))
}
