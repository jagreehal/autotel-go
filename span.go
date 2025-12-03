package autotel

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span wraps an OpenTelemetry span with ergonomic helpers
type Span interface {
	// SetAttribute sets an attribute (supports string, int, int64, float64, bool, and slices of these)
	SetAttribute(key string, value any)

	// AddEvent adds an event to the span
	AddEvent(name string, attrs ...attribute.KeyValue)

	// AddLink adds a link to another span (useful for connecting causally related spans)
	AddLink(link trace.Link)

	// AddLinks adds multiple links to other spans
	AddLinks(links ...trace.Link)

	// UpdateName updates the span name dynamically
	UpdateName(name string)

	// SetStatus sets the span status
	SetStatus(code codes.Code, description string)

	// RecordError records an error and sets span status to ERROR
	RecordError(err error)

	// End ends the span
	End()

	// SpanContext returns the span context
	SpanContext() trace.SpanContext

	// IsRecording returns whether the span is recording
	IsRecording() bool
}

// spanImpl implements the Span interface
type spanImpl struct {
	span trace.Span
}

func (s *spanImpl) SetAttribute(key string, value any) {
	if !s.span.IsRecording() {
		return
	}

	// Convert value to string for PII redaction check
	strValue := fmt.Sprintf("%v", value)

	// Apply PII redaction if enabled
	mu.RLock()
	pr := globalPIIRedactor
	mu.RUnlock()
	if pr != nil {
		originalValue := strValue
		strValue = pr.Redact(key, strValue)
		if strValue != originalValue {
			debugPrint("  🔒 PII redacted: %s [original=%v, redacted=%s]", key, value, strValue)
		}
	}

	// Convert value to OpenTelemetry attribute
	var attr attribute.KeyValue
	switch v := value.(type) {
	case string:
		// Use redacted value if it was modified
		if pr != nil {
			attr = attribute.String(key, strValue)
		} else {
			attr = attribute.String(key, v)
		}
	case int:
		attr = attribute.Int(key, v)
	case int64:
		attr = attribute.Int64(key, v)
	case float64:
		attr = attribute.Float64(key, v)
	case bool:
		attr = attribute.Bool(key, v)
	// Array types (OTel spec compliant)
	case []string:
		attr = attribute.StringSlice(key, v)
	case []int:
		attr = attribute.IntSlice(key, v)
	case []int64:
		attr = attribute.Int64Slice(key, v)
	case []float64:
		attr = attribute.Float64Slice(key, v)
	case []bool:
		attr = attribute.BoolSlice(key, v)
	default:
		// Use redacted string value
		attr = attribute.String(key, strValue)
	}

	s.span.SetAttributes(attr)
	debugSpanAttribute(s.span.SpanContext(), key, value)
}

func (s *spanImpl) AddEvent(name string, attrs ...attribute.KeyValue) {
	if !s.span.IsRecording() {
		return
	}
	s.span.AddEvent(name, trace.WithAttributes(attrs...))
}

func (s *spanImpl) AddLink(link trace.Link) {
	if !s.span.IsRecording() {
		return
	}
	// Note: OTel Go SDK doesn't have a direct AddLink method on Span.
	// Links are typically added at span creation time via trace.WithLinks().
	// This is a limitation of the Go SDK compared to the JS SDK.
	// We record this as an event to preserve the link information.
	s.span.AddEvent("span.link",
		trace.WithAttributes(
			attribute.String("linked_trace_id", link.SpanContext.TraceID().String()),
			attribute.String("linked_span_id", link.SpanContext.SpanID().String()),
		),
	)
}

func (s *spanImpl) AddLinks(links ...trace.Link) {
	for _, link := range links {
		s.AddLink(link)
	}
}

func (s *spanImpl) UpdateName(name string) {
	if !s.span.IsRecording() {
		return
	}
	s.span.SetName(name)
}

func (s *spanImpl) SetStatus(code codes.Code, description string) {
	if !s.span.IsRecording() {
		return
	}
	s.span.SetStatus(code, description)
}

func (s *spanImpl) RecordError(err error) {
	if !s.span.IsRecording() || err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
	debugSpanError(s.span.SpanContext(), err)
}

func (s *spanImpl) End() {
	if s.span.IsRecording() {
		// Note: OpenTelemetry doesn't expose status before End(), so we'll log basic info
		debugPrint("← End span [trace_id=%s, span_id=%s]", s.span.SpanContext().TraceID().String(), s.span.SpanContext().SpanID().String())
	}
	s.span.End()
}

func (s *spanImpl) SpanContext() trace.SpanContext {
	return s.span.SpanContext()
}

func (s *spanImpl) IsRecording() bool {
	return s.span.IsRecording()
}

// noopSpan is a no-op span implementation for rate-limited or disabled spans
type noopSpan struct{}

func (n *noopSpan) SetAttribute(key string, value any)                {}
func (n *noopSpan) AddEvent(name string, attrs ...attribute.KeyValue) {}
func (n *noopSpan) AddLink(link trace.Link)                           {}
func (n *noopSpan) AddLinks(links ...trace.Link)                      {}
func (n *noopSpan) UpdateName(name string)                            {}
func (n *noopSpan) SetStatus(code codes.Code, description string)     {}
func (n *noopSpan) RecordError(err error)                             {}
func (n *noopSpan) End()                                              {}
func (n *noopSpan) SpanContext() trace.SpanContext                    { return trace.SpanContext{} }
func (n *noopSpan) IsRecording() bool                                 { return false }
