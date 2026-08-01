package processors

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// AttributeRedactorFn returns a redacted value for the given attribute key and value.
// It can return the original value or a redacted string (e.g. "[REDACTED]").
type AttributeRedactorFn func(key string, value interface{}) interface{}

// AttributeRedactingProcessor wraps the next processor. In the Go SDK, span data
// is read-only when OnEnd is called, so this processor currently forwards spans
// unchanged. PII redaction is already supported at the global level via WithPIIRedaction;
// this processor exists for API parity. A future version may integrate with an exporter
// wrapper to redact attributes per this function.
type AttributeRedactingProcessor struct {
	redactor AttributeRedactorFn
	next     sdktrace.SpanProcessor
}

// NewAttributeRedactingProcessor creates a processor that forwards spans to next.
// The redactor is retained for API parity; global PII redaction is available via autotel.WithPIIRedaction.
func NewAttributeRedactingProcessor(redactor AttributeRedactorFn, next sdktrace.SpanProcessor) *AttributeRedactingProcessor {
	if redactor == nil {
		redactor = func(key string, value interface{}) interface{} { return value }
	}
	return &AttributeRedactingProcessor{redactor: redactor, next: next}
}

// OnStart forwards to the next processor.
func (p *AttributeRedactingProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor (attribute redaction is applied globally via PIIRedactor when set).
func (p *AttributeRedactingProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.next.OnEnd(s)
}

// Shutdown shuts down the next processor.
func (p *AttributeRedactingProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

// ForceFlush flushes the next processor.
func (p *AttributeRedactingProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}
