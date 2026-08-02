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
	next   sdktrace.SpanProcessor
	policy TailPolicy
}

// TailPolicy decides whether an ended span is kept. It runs only for spans that
// were not already decided by the sampling.tail.keep attribute.
type TailPolicy func(sdktrace.ReadOnlySpan) bool

// TailSamplingOption configures a TailSamplingSpanProcessor.
type TailSamplingOption func(*TailSamplingSpanProcessor)

// WithTailPolicy sets the policy applied to spans that carry no explicit
// sampling.tail.keep decision. Without one the processor only honours spans
// marked by hand, which is the behaviour it shipped with.
func WithTailPolicy(policy TailPolicy) TailSamplingOption {
	return func(p *TailSamplingSpanProcessor) { p.policy = policy }
}

// NewTailSamplingSpanProcessor creates a tail sampling processor that wraps next.
func NewTailSamplingSpanProcessor(next sdktrace.SpanProcessor, opts ...TailSamplingOption) *TailSamplingSpanProcessor {
	p := &TailSamplingSpanProcessor{next: next}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// OnStart forwards to the next processor.
func (p *TailSamplingSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor unless the span is dropped, either by an
// explicit sampling.tail.evaluated=true / sampling.tail.keep=false pair or by the
// configured policy. An explicit decision wins: code that marked a span by hand
// meant it, and a policy should not overrule it.
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
	if evaluated {
		if !keep {
			return
		}
		p.next.OnEnd(s)
		return
	}
	if p.policy != nil && !p.policy(s) {
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
