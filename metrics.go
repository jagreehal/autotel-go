package autotel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric wraps an OTEL meter to provide lightweight counters/histograms with trace correlation.
//
// Trace correlation uses exemplars: the SDK samples trace_id/span_id from ctx
// onto the data point. They are never metric attributes, since a per-request
// attribute creates a new time series per request.
type Metric struct {
	meter metric.Meter
}

// Meter returns a Metric helper tied to the current global provider.
// It looks the meter up on every call (the SDK caches it) so a later Init,
// or a test that swaps providers, is never stuck with a stale one.
func Meter() Metric {
	return Metric{meter: otel.Meter(tracerName)}
}

// Counter adds a delta to a named counter. A span in ctx becomes an exemplar.
func (m Metric) Counter(ctx context.Context, name string, value float64, attrs map[string]any) {
	c, err := m.meter.Float64Counter(name)
	if err != nil {
		return
	}
	c.Add(ctx, value, metric.WithAttributes(toAttributes(attrs)...))
}

// Histogram records a value to a named histogram. A span in ctx becomes an exemplar.
func (m Metric) Histogram(ctx context.Context, name string, value float64, attrs map[string]any) {
	h, err := m.meter.Float64Histogram(name)
	if err != nil {
		return
	}
	h.Record(ctx, value, metric.WithAttributes(toAttributes(attrs)...))
}

func toAttributes(attrs map[string]any) []attribute.KeyValue {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kvs = append(kvs, attributeFromValue(k, v))
	}
	return kvs
}
