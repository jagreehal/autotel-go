package testing

import (
	"context"
	"sync"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// InMemoryExporter is a span exporter that stores spans in memory for testing.
type InMemoryExporter struct {
	mu    sync.RWMutex
	spans []sdktrace.ReadOnlySpan
}

// NewInMemoryExporter creates a new in-memory exporter.
func NewInMemoryExporter() *InMemoryExporter {
	return &InMemoryExporter{
		spans: make([]sdktrace.ReadOnlySpan, 0),
	}
}

// ExportSpans stores spans in memory.
func (e *InMemoryExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

// Shutdown implements the SpanExporter interface.
func (e *InMemoryExporter) Shutdown(ctx context.Context) error {
	e.Reset()
	return nil
}

// GetSpans returns all exported spans.
func (e *InMemoryExporter) GetSpans() []sdktrace.ReadOnlySpan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.spans
}

// Reset clears all stored spans.
func (e *InMemoryExporter) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = make([]sdktrace.ReadOnlySpan, 0)
}

// GetSpanByName returns the first span with the given name.
func (e *InMemoryExporter) GetSpanByName(name string) (sdktrace.ReadOnlySpan, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, span := range e.spans {
		if span.Name() == name {
			return span, true
		}
	}
	return nil, false
}
