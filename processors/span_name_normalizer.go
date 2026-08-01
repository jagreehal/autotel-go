package processors

import (
	"context"
	"regexp"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SpanNameNormalizerFn returns a normalized span name. Used to rewrite span names
// (e.g. strip query params, normalize HTTP routes) before export.
type SpanNameNormalizerFn func(name string) string

// SpanNameNormalizingProcessor wraps the next processor. In the Go SDK, span data
// is read-only when OnEnd is called, so this processor currently forwards spans
// unchanged. A future version may integrate with an exporter wrapper to apply
// normalizations. Use WithSpanNameNormalizer to register patterns or a custom function.
type SpanNameNormalizingProcessor struct {
	normalize SpanNameNormalizerFn
	next      sdktrace.SpanProcessor
}

// NewSpanNameNormalizingProcessor creates a processor that forwards spans to next.
// The normalize function is retained for API parity; in Go, full name rewriting
// would require exporter-level support.
func NewSpanNameNormalizingProcessor(normalize SpanNameNormalizerFn, next sdktrace.SpanProcessor) *SpanNameNormalizingProcessor {
	if normalize == nil {
		normalize = func(name string) string { return name }
	}
	return &SpanNameNormalizingProcessor{normalize: normalize, next: next}
}

// OnStart forwards to the next processor.
func (p *SpanNameNormalizingProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor (name normalization would require exporter-level support).
func (p *SpanNameNormalizingProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.next.OnEnd(s)
}

// Shutdown shuts down the next processor.
func (p *SpanNameNormalizingProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

// ForceFlush flushes the next processor.
func (p *SpanNameNormalizingProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}

// NormalizerPresetHTTP returns a normalizer that replaces path segments that look like IDs with a placeholder.
func NormalizerPresetHTTP(name string) string {
	// Replace common ID-like segments (hex, UUIDs, numbers) with placeholders
	hexRe := regexp.MustCompile(`/[0-9a-fA-F]{8,}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}?`)
	numRe := regexp.MustCompile(`/\d+`)
	name = hexRe.ReplaceAllString(name, "/:id")
	name = numRe.ReplaceAllString(name, "/:id")
	return name
}
