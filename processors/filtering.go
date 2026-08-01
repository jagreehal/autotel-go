// Package processors provides span processors for filtering, tail sampling, and related pipeline stages.
package processors

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SpanFilterPredicate returns true if the span should be kept (forwarded to the next processor).
// When it returns false, the span is dropped and not exported.
type SpanFilterPredicate func(sdktrace.ReadOnlySpan) bool

// FilteringSpanProcessor wraps another SpanProcessor and forwards spans only when the predicate returns true.
type FilteringSpanProcessor struct {
	predicate SpanFilterPredicate
	next      sdktrace.SpanProcessor
}

// NewFilteringSpanProcessor creates a processor that drops spans for which predicate returns false.
func NewFilteringSpanProcessor(predicate SpanFilterPredicate, next sdktrace.SpanProcessor) *FilteringSpanProcessor {
	return &FilteringSpanProcessor{predicate: predicate, next: next}
}

// OnStart forwards to the next processor.
func (p *FilteringSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor only when predicate(span) is true.
func (p *FilteringSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	if p.predicate != nil && !p.predicate(s) {
		return
	}
	p.next.OnEnd(s)
}

// Shutdown shuts down the next processor.
func (p *FilteringSpanProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

// ForceFlush flushes the next processor.
func (p *FilteringSpanProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}
