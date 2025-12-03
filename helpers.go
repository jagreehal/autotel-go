package autotel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// SetDuration sets the duration of an operation as a span attribute.
// This is useful for tracking operation performance.
//
// Example:
//
//	start := time.Now()
//	// ... do work ...
//	autotel.SetDuration(span, start)
func SetDuration(span Span, start time.Time) {
	duration := time.Since(start)
	span.SetAttribute("duration_ms", duration.Milliseconds())
	span.SetAttribute("duration_ns", duration.Nanoseconds())
}

// SetHTTPRequestAttributes sets common HTTP request attributes on a span.
// This is a convenience function for HTTP handlers.
//
// Example:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    ctx, span := autotel.Start(r.Context(), "handleRequest")
//	    defer span.End()
//	    autotel.SetHTTPRequestAttributes(span, r)
//	    // ... handle request ...
//	}
func SetHTTPRequestAttributes(span Span, method, path, userAgent string) {
	span.SetAttribute("http.method", method)
	span.SetAttribute("http.path", path)
	if userAgent != "" {
		span.SetAttribute("http.user_agent", userAgent)
	}
}

// AddEventWithAttributes is a convenience function for adding events with attributes.
//
// Example:
//
//	autotel.AddEventWithAttributes(span, "cache_hit",
//	    "cache.key", "user:123",
//	    "cache.ttl", 3600,
//	)
func AddEventWithAttributes(span Span, name string, attrs ...any) {
	if len(attrs)%2 != 0 {
		return // Invalid attribute pairs
	}

	otelAttrs := make([]attribute.KeyValue, 0, len(attrs)/2)
	for i := 0; i < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		value := attrs[i+1]
		otelAttrs = append(otelAttrs, attributeFromValue(key, value))
	}

	span.AddEvent(name, otelAttrs...)
}

// attributeFromValue converts a value to an OpenTelemetry attribute.
func attributeFromValue(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, v)
	case int:
		return attribute.Int(key, v)
	case int64:
		return attribute.Int64(key, v)
	case float64:
		return attribute.Float64(key, v)
	case bool:
		return attribute.Bool(key, v)
	default:
		return attribute.String(key, attributeValueToString(value))
	}
}

// attributeValueToString converts a value to string for attribute.
func attributeValueToString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// IsTracingEnabled checks if tracing is currently enabled.
// Returns true if a TracerProvider is set and not a no-op provider.
func IsTracingEnabled(ctx context.Context) bool {
	_, span := Start(ctx, "_check")
	defer span.End()
	return span.IsRecording()
}

// SetAttribute sets a single attribute on the current span from context.
// This is a convenience function that doesn't require getting the span first.
func SetAttribute(ctx context.Context, key string, value any) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		// Create a temporary span wrapper to use SetAttribute which handles PII redaction
		spanImpl := &spanImpl{span: span}
		spanImpl.SetAttribute(key, value)
	}
}

// SetAttributes sets multiple attributes on the current span from context.
// This is a convenience function that doesn't require getting the span first.
func SetAttributes(ctx context.Context, attrs map[string]any) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		spanImpl := &spanImpl{span: span}
		for k, v := range attrs {
			spanImpl.SetAttribute(k, v)
		}
	}
}

// AddEvent adds an event to the current span from context.
// This is a convenience function that doesn't require getting the span first.
func AddEvent(ctx context.Context, name string, attrs map[string]any) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		otelAttrs := make([]attribute.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			otelAttrs = append(otelAttrs, attributeFromValue(k, v))
		}
		span.AddEvent(name, trace.WithAttributes(otelAttrs...))
	}
}

// RecordError records an error on the current span from context.
// This sets the span status to ERROR automatically.
// This is a convenience function that doesn't require getting the span first.
func RecordError(ctx context.Context, err error, attrs map[string]any) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() && err != nil {
		if len(attrs) > 0 {
			otelAttrs := make([]attribute.KeyValue, 0, len(attrs))
			for k, v := range attrs {
				otelAttrs = append(otelAttrs, attributeFromValue(k, v))
			}
			span.AddEvent("exception", trace.WithAttributes(otelAttrs...))
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// GetTraceID returns the current trace ID as a hex string.
// Returns empty string if no active span exists.
func GetTraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		sc := span.SpanContext()
		if sc.IsValid() {
			return fmt.Sprintf("%032x", sc.TraceID())
		}
	}
	return ""
}

// GetSpanID returns the current span ID as a hex string.
// Returns empty string if no active span exists.
func GetSpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		sc := span.SpanContext()
		if sc.IsValid() {
			return fmt.Sprintf("%016x", sc.SpanID())
		}
	}
	return ""
}
