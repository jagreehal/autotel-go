package sampling

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// CreateLinkFromHeaders creates an OpenTelemetry Link from W3C trace context headers.
// This is useful for message consumers that need to link to the producer span.
//
// The headers map should contain "traceparent" and optionally "tracestate" keys.
// Returns the link and true if the context was valid, or an empty link and false otherwise.
//
// Example:
//
//	headers := map[string]string{
//	    "traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
//	}
//	link, ok := sampling.CreateLinkFromHeaders(headers)
//	if ok {
//	    ctx, span := tracer.Start(ctx, "process-message", trace.WithLinks(link))
//	    defer span.End()
//	}
func CreateLinkFromHeaders(headers map[string]string, attrs ...attribute.KeyValue) (trace.Link, bool) {
	if headers == nil {
		return trace.Link{}, false
	}

	// Use W3C TraceContext propagator to extract trace context
	propagator := propagation.TraceContext{}
	carrier := propagation.MapCarrier(headers)
	ctx := propagator.Extract(context.Background(), carrier)

	// Get the span context from the extracted context
	spanCtx := trace.SpanContextFromContext(ctx)

	if !spanCtx.IsValid() {
		return trace.Link{}, false
	}

	return trace.Link{
		SpanContext: spanCtx,
		Attributes:  attrs,
	}, true
}

// ExtractLinksFromBatch extracts Links from a batch of messages for fan-in scenarios.
// This is useful for batch processing where multiple producer spans should be linked.
//
// The generic function accepts any message type and a getter function to extract headers.
// Only messages with valid trace context will produce links.
//
// Example:
//
//	type Message struct {
//	    Body    string
//	    Headers map[string]string
//	}
//
//	messages := []Message{
//	    {Body: "msg1", Headers: map[string]string{"traceparent": "..."}},
//	    {Body: "msg2", Headers: map[string]string{"traceparent": "..."}},
//	}
//
//	links := sampling.ExtractLinksFromBatch(messages, func(m Message) map[string]string {
//	    return m.Headers
//	})
//
//	ctx, span := tracer.Start(ctx, "process-batch", trace.WithLinks(links...))
func ExtractLinksFromBatch[T any](messages []T, getHeaders func(T) map[string]string) []trace.Link {
	links := make([]trace.Link, 0, len(messages))

	for _, msg := range messages {
		headers := getHeaders(msg)
		if link, ok := CreateLinkFromHeaders(headers); ok {
			links = append(links, link)
		}
	}

	return links
}

// CreateLinkFromContext creates a Link from an existing context containing a span.
// This is a convenience wrapper around trace.LinkFromContext for common use cases.
//
// Example:
//
//	// Link to the current span in some producer context
//	link := sampling.CreateLinkFromContext(producerCtx)
//	ctx, span := tracer.Start(ctx, "consumer", trace.WithLinks(link))
func CreateLinkFromContext(ctx context.Context, attrs ...attribute.KeyValue) trace.Link {
	return trace.LinkFromContext(ctx, attrs...)
}
