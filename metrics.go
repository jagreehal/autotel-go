package autotel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric wraps an OTEL meter to provide lightweight counters/histograms with trace correlation.
type Metric struct {
	meter metric.Meter
}

var (
	metricOnce  sync.Once
	globalMeter metric.Meter
)

// Meter returns a Metric helper tied to the global provider.
func Meter() Metric {
	metricOnce.Do(func() {
		globalMeter = otel.Meter(tracerName)
	})
	return Metric{meter: globalMeter}
}

// Counter adds a delta to a named counter, attaching trace context if present.
func (m Metric) Counter(ctx context.Context, name string, value float64, attrs map[string]any) {
	c, err := m.meter.Float64Counter(name)
	if err != nil {
		return
	}
	var opts []metric.AddOption
	for k, v := range attrs {
		opts = append(opts, metric.WithAttributes(attributeFromValue(k, v)))
	}
	if tid := GetTraceID(ctx); tid != "" {
		opts = append(opts, metric.WithAttributes(attribute.String("trace_id", tid)))
	}
	if sid := GetSpanID(ctx); sid != "" {
		opts = append(opts, metric.WithAttributes(attribute.String("span_id", sid)))
	}
	c.Add(ctx, value, opts...)
}

// Histogram records a value to a named histogram.
func (m Metric) Histogram(ctx context.Context, name string, value float64, attrs map[string]any) {
	h, err := m.meter.Float64Histogram(name)
	if err != nil {
		return
	}
	var opts []metric.RecordOption
	for k, v := range attrs {
		opts = append(opts, metric.WithAttributes(attributeFromValue(k, v)))
	}
	if tid := GetTraceID(ctx); tid != "" {
		opts = append(opts, metric.WithAttributes(attribute.String("trace_id", tid)))
	}
	if sid := GetSpanID(ctx); sid != "" {
		opts = append(opts, metric.WithAttributes(attribute.String("span_id", sid)))
	}
	h.Record(ctx, value, opts...)
}
