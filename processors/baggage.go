package processors

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// DefaultBaggagePrefix is prepended to baggage keys copied onto spans.
const DefaultBaggagePrefix = "baggage."

// BaggageKeyFilter reports whether a baggage key should be copied onto spans.
type BaggageKeyFilter func(key string) bool

// BaggageSpanProcessor copies baggage entries onto span attributes at span start,
// so business context propagated through baggage is visible in a trace UI without
// setting the same attributes by hand at every call site.
//
// Attributes are written in OnStart because span attributes are read-only by the
// time OnEnd runs. That means a span only carries the baggage that was present
// when it started; baggage added later in the operation is picked up by
// subsequent child spans, not retroactively by this one.
//
// Baggage crosses service boundaries in a header, so treat it as untrusted input
// from anything outside your perimeter. Use a filter to copy only the keys you
// expect rather than everything an upstream caller chose to send.
type BaggageSpanProcessor struct {
	prefix string
	filter BaggageKeyFilter
	next   sdktrace.SpanProcessor
}

// BaggageSpanProcessorOption configures a BaggageSpanProcessor.
type BaggageSpanProcessorOption func(*BaggageSpanProcessor)

// WithBaggagePrefix sets the prefix for copied attributes. The default is
// "baggage.", which keeps propagated context distinguishable from attributes the
// service set itself. Pass an empty string to copy keys unchanged.
func WithBaggagePrefix(prefix string) BaggageSpanProcessorOption {
	return func(p *BaggageSpanProcessor) {
		p.prefix = prefix
	}
}

// WithBaggageKeyFilter copies only the keys for which keep returns true.
func WithBaggageKeyFilter(keep BaggageKeyFilter) BaggageSpanProcessorOption {
	return func(p *BaggageSpanProcessor) {
		p.filter = keep
	}
}

// WithBaggageAllowlist copies only the listed baggage keys. Prefer this at a trust
// boundary: it bounds both what an upstream service can write onto your spans and
// the attribute cardinality it can create.
func WithBaggageAllowlist(keys ...string) BaggageSpanProcessorOption {
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	return WithBaggageKeyFilter(func(key string) bool { return allowed[key] })
}

// NewBaggageSpanProcessor creates a processor that copies baggage onto spans and
// forwards them to next.
func NewBaggageSpanProcessor(next sdktrace.SpanProcessor, opts ...BaggageSpanProcessorOption) *BaggageSpanProcessor {
	p := &BaggageSpanProcessor{
		prefix: DefaultBaggagePrefix,
		next:   next,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// OnStart copies the baggage carried by parent onto the span, then forwards.
func (p *BaggageSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	members := baggage.FromContext(parent).Members()
	if len(members) > 0 {
		attrs := make([]attribute.KeyValue, 0, len(members))
		for _, member := range members {
			key := member.Key()
			if p.filter != nil && !p.filter(key) {
				continue
			}
			attrs = append(attrs, attribute.String(p.prefix+key, member.Value()))
		}
		if len(attrs) > 0 {
			s.SetAttributes(attrs...)
		}
	}

	p.next.OnStart(parent, s)
}

// OnEnd forwards to the next processor.
func (p *BaggageSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.next.OnEnd(s)
}

// Shutdown shuts down the next processor.
func (p *BaggageSpanProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

// ForceFlush flushes the next processor.
func (p *BaggageSpanProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}

// BaggagePrefixFilter returns a filter matching keys with the given prefix, for
// the common case of namespacing your own baggage (for example "app.").
func BaggagePrefixFilter(prefix string) BaggageKeyFilter {
	return func(key string) bool { return strings.HasPrefix(key, prefix) }
}
