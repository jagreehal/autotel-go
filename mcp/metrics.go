package mcp

import (
	"context"

	"github.com/jagreehal/autotel-go/v2"
)

// Duration bucket boundaries in seconds (from OTel MCP spec).
var DurationBucketsSeconds = []float64{
	0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300,
}

// RecordClientOperationDuration records the duration of an MCP client operation.
// durationS is the operation duration in seconds; attrs can include mcp.method.name, gen_ai.tool.name, etc.
func RecordClientOperationDuration(ctx context.Context, durationS float64, attrs map[string]any) {
	m := autotel.Meter()
	m.Histogram(ctx, MetricClientOperationDuration, durationS, attrs)
}

// RecordServerOperationDuration records the duration of an MCP server operation.
func RecordServerOperationDuration(ctx context.Context, durationS float64, attrs map[string]any) {
	m := autotel.Meter()
	m.Histogram(ctx, MetricServerOperationDuration, durationS, attrs)
}
