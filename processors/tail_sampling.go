package processors

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Tail sampling attribute keys. When a span has TailEvaluatedKey=true and TailKeepKey=false,
// it is dropped (not forwarded to the next processor).
const (
	TailEvaluatedKey = "sampling.tail.evaluated"
	TailKeepKey      = "sampling.tail.keep"
)

// TailSamplingSpanProcessor wraps another SpanProcessor and drops spans that have been
// marked for drop via attributes: sampling.tail.evaluated=true and sampling.tail.keep=false.
// This allows head sampling to accept a span, then after the operation completes a decorator
// sets these attributes to decide whether to keep or drop.
type TailSamplingSpanProcessor struct {
	next sdktrace.SpanProcessor
}

// NewTailSamplingSpanProcessor creates a tail sampling processor that wraps next.
func NewTailSamplingSpanProcessor(next sdktrace.SpanProcessor) *TailSamplingSpanProcessor {
	return &TailSamplingSpanProcessor{next: next}
}

// OnStart forwards to the next processor.
func (p *TailSamplingSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor unless the span has sampling.tail.evaluated=true
// and sampling.tail.keep=false (in which case the span is dropped).
func (p *TailSamplingSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	var evaluated, keep bool
	for _, kv := range s.Attributes() {
		switch kv.Key {
		case TailEvaluatedKey:
			evaluated = kv.Value.AsBool()
		case TailKeepKey:
			keep = kv.Value.AsBool()
		}
	}
	if evaluated && !keep {
		return
	}
	p.next.OnEnd(s)
}

// Shutdown shuts down the next processor.
func (p *TailSamplingSpanProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

// ForceFlush flushes the next processor.
func (p *TailSamplingSpanProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}
