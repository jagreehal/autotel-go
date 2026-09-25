package autotel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
)

// Init installs the W3C propagator, so otelhttp continues the trace named in
// the incoming traceparent header.
func TestInitJoinsIncomingTraceparent(t *testing.T) {
	exporter := initWithExporter(t)

	handler := middleware.HTTPMiddleware("svc")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	time.Sleep(150 * time.Millisecond)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no server span exported")
	}
	if got, want := spans[0].SpanContext.TraceID().String(), "4bf92f3577b34da6a3ce929d0e0e4736"; got != want {
		t.Errorf("server span trace ID = %s, want %s from the traceparent header", got, want)
	}
}

// metricRecorder is a metric exporter that remembers which instruments it saw.
type metricRecorder struct {
	mu    sync.Mutex
	names []string
}

func (r *metricRecorder) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (r *metricRecorder) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (r *metricRecorder) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			r.names = append(r.names, m.Name)
		}
	}
	return nil
}

func (r *metricRecorder) ForceFlush(context.Context) error { return nil }
func (r *metricRecorder) Shutdown(context.Context) error   { return nil }

// cleanup shuts down the MeterProvider, so the periodic reader exports what it
// holds before the process exits.
func TestCleanupFlushesMetrics(t *testing.T) {
	rec := &metricRecorder{}
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("metrics-flush"),
		autotel.WithDebug(false),
		autotel.WithMetricExporters(rec),
		autotel.WithMetricInterval(time.Hour), // the reader never fires on its own
	)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	autotel.Meter().Counter(context.Background(), "orders.created", 1, nil)
	cleanup()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !contains(rec.names, "orders.created") {
		t.Errorf("counter not flushed on cleanup; exported %v", rec.names)
	}
}

type logRecord struct {
	body    string
	traceID oteltrace.TraceID
}

// logRecorder is a log exporter that keeps the body and trace ID of each record.
type logRecorder struct {
	mu      sync.Mutex
	records []logRecord
}

func (r *logRecorder) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		r.records = append(r.records, logRecord{body: rec.Body().AsString(), traceID: rec.TraceID()})
	}
	return nil
}

func (r *logRecorder) ForceFlush(context.Context) error { return nil }
func (r *logRecorder) Shutdown(context.Context) error   { return nil }

func TestLoggerExportsWithTraceContext(t *testing.T) {
	tests := []struct {
		name     string
		opts     []autotel.Option
		env      string
		wantLogs bool
	}{
		{name: "enabled by default", wantLogs: true},
		{name: "WithLogs(false)", opts: []autotel.Option{autotel.WithLogs(false)}},
		{name: "OTEL_LOGS_EXPORTER=none", env: "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_LOGS_EXPORTER", tt.env)
			rec := &logRecorder{}
			cleanup, err := autotel.Init(context.Background(), append([]autotel.Option{
				autotel.WithService("logs"),
				autotel.WithDebug(false),
				autotel.WithMetrics(false),
				autotel.WithSampler(sdktrace.AlwaysSample()),
				autotel.WithLogExporters(rec),
			}, tt.opts...)...)
			if err != nil {
				t.Fatalf("Init: %v", err)
			}

			ctx, span := autotel.Start(context.Background(), "checkout")
			autotel.Logger("test").InfoContext(ctx, "order placed")
			span.End()
			cleanup()

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if !tt.wantLogs {
				if len(rec.records) != 0 {
					t.Errorf("log export disabled, but exported %d records", len(rec.records))
				}
				return
			}
			if len(rec.records) != 1 {
				t.Fatalf("exported %d records, want 1", len(rec.records))
			}
			got := rec.records[0]
			want := oteltrace.SpanContextFromContext(ctx).TraceID()
			if got.body != "order placed" || got.traceID != want {
				t.Errorf("record = {%q, %s}, want {%q, %s}", got.body, got.traceID, "order placed", want)
			}
		})
	}
}
