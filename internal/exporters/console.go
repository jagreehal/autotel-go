package exporters

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ConsoleExporter prints spans to stderr in JSON for quick debug mode parity.
type ConsoleExporter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewConsoleExporter creates a console span exporter.
func NewConsoleExporter() *ConsoleExporter {
	return &ConsoleExporter{enc: json.NewEncoder(os.Stderr)}
}

// ExportSpans writes spans as structured JSON.
func (c *ConsoleExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, span := range spans {
		attrs := span.Attributes()
		attrMap := make(map[string]any, len(attrs))
		for _, a := range attrs {
			attrMap[string(a.Key)] = a.Value.AsInterface()
		}

		events := span.Events()
		eventList := make([]map[string]any, 0, len(events))
		for _, ev := range events {
			eventAttrs := make(map[string]any, len(ev.Attributes))
			for _, ea := range ev.Attributes {
				eventAttrs[string(ea.Key)] = ea.Value.AsInterface()
			}
			eventList = append(eventList, map[string]any{
				"name":       ev.Name,
				"attributes": eventAttrs,
				"timestamp":  ev.Time,
			})
		}

		payload := map[string]any{
			"name":       span.Name(),
			"trace_id":   span.SpanContext().TraceID().String(),
			"span_id":    span.SpanContext().SpanID().String(),
			"parent_id":  span.Parent().SpanID().String(),
			"start":      span.StartTime().Format(time.RFC3339Nano),
			"end":        span.EndTime().Format(time.RFC3339Nano),
			"status":     span.Status().Code.String(),
			"attributes": attrMap,
			"events":     eventList,
		}

		_ = c.enc.Encode(payload)
	}

	return nil
}

// Shutdown is a no-op for console exporter.
func (c *ConsoleExporter) Shutdown(_ context.Context) error {
	return nil
}
