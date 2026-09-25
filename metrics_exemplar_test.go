package autotel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Two requests in two traces must land on one time series. Trace IDs belong
// on exemplars; as attributes they would create a series per request.
func TestMeterCorrelatesViaExemplarsNotAttributes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tracer := sdktrace.NewTracerProvider().Tracer("test")
	for range 2 {
		ctx, span := tracer.Start(context.Background(), "req")
		Meter().Counter(ctx, "exemplar.requests", 1, map[string]any{"route": "/x"})
		span.End()
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "exemplar.requests" {
				continue
			}
			pts := m.Data.(metricdata.Sum[float64]).DataPoints
			if len(pts) != 1 {
				t.Fatalf("got %d series, want 1 (attrs: %v)", len(pts), pts[0].Attributes.Encoded(nil))
			}
			if pts[0].Value != 2 {
				t.Errorf("value = %v, want 2", pts[0].Value)
			}
			if _, ok := pts[0].Attributes.Value("trace_id"); ok {
				t.Error("trace_id is a metric attribute, want it only on exemplars")
			}
			if len(pts[0].Exemplars) == 0 || len(pts[0].Exemplars[0].TraceID) == 0 {
				t.Error("want an exemplar carrying the trace ID")
			}
			return
		}
	}
	t.Fatal("metric exemplar.requests not collected")
}
