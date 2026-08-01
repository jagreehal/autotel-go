package sampling

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestCreateLinkFromHeaders_ValidTraceparent(t *testing.T) {
	headers := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}

	link, ok := CreateLinkFromHeaders(headers)

	require.True(t, ok, "should return true for valid traceparent")
	assert.True(t, link.SpanContext.IsValid(), "link should have valid span context")
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", link.SpanContext.TraceID().String())
	assert.Equal(t, "b7ad6b7169203331", link.SpanContext.SpanID().String())
	assert.True(t, link.SpanContext.IsSampled(), "should be sampled (flag 01)")
}

func TestCreateLinkFromHeaders_ValidTraceparentUnsampled(t *testing.T) {
	headers := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00",
	}

	link, ok := CreateLinkFromHeaders(headers)

	require.True(t, ok, "should return true for valid traceparent")
	assert.False(t, link.SpanContext.IsSampled(), "should NOT be sampled (flag 00)")
}

func TestCreateLinkFromHeaders_WithAttributes(t *testing.T) {
	headers := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}

	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination", "orders"),
	}

	link, ok := CreateLinkFromHeaders(headers, attrs...)

	require.True(t, ok)
	assert.Len(t, link.Attributes, 2)
	assert.Equal(t, "messaging.system", string(link.Attributes[0].Key))
	assert.Equal(t, "kafka", link.Attributes[0].Value.AsString())
}

func TestCreateLinkFromHeaders_InvalidTraceparent(t *testing.T) {
	testCases := []struct {
		name    string
		headers map[string]string
	}{
		{"empty headers", map[string]string{}},
		{"nil headers", nil},
		{"invalid format", map[string]string{"traceparent": "invalid"}},
		{"wrong version", map[string]string{"traceparent": "ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}},
		{"too short", map[string]string{"traceparent": "00-abc-def-01"}},
		{"missing traceparent", map[string]string{"tracestate": "foo=bar"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			link, ok := CreateLinkFromHeaders(tc.headers)
			assert.False(t, ok, "should return false for invalid traceparent")
			assert.False(t, link.SpanContext.IsValid(), "link should have invalid span context")
		})
	}
}

func TestCreateLinkFromHeaders_WithTracestate(t *testing.T) {
	headers := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"tracestate":  "vendor1=value1,vendor2=value2",
	}

	link, ok := CreateLinkFromHeaders(headers)

	require.True(t, ok)
	assert.True(t, link.SpanContext.IsValid())
	// TraceState should be preserved
	ts := link.SpanContext.TraceState()
	assert.Equal(t, "value1", ts.Get("vendor1"))
	assert.Equal(t, "value2", ts.Get("vendor2"))
}

func TestExtractLinksFromBatch(t *testing.T) {
	type Message struct {
		Body    string
		Headers map[string]string
	}

	messages := []Message{
		{
			Body: "message 1",
			Headers: map[string]string{
				"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			},
		},
		{
			Body: "message 2",
			Headers: map[string]string{
				"traceparent": "00-11111111111111111111111111111111-2222222222222222-01",
			},
		},
		{
			Body:    "message 3 - no headers",
			Headers: nil,
		},
	}

	links := ExtractLinksFromBatch(messages, func(m Message) map[string]string {
		return m.Headers
	})

	// Should have 2 valid links (message 3 has no headers)
	assert.Len(t, links, 2)
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", links[0].SpanContext.TraceID().String())
	assert.Equal(t, "11111111111111111111111111111111", links[1].SpanContext.TraceID().String())
}

func TestExtractLinksFromBatch_EmptyBatch(t *testing.T) {
	type Message struct {
		Headers map[string]string
	}

	var messages []Message

	links := ExtractLinksFromBatch(messages, func(m Message) map[string]string {
		return m.Headers
	})

	assert.Empty(t, links)
}

func TestExtractLinksFromBatch_AllInvalid(t *testing.T) {
	type Message struct {
		Headers map[string]string
	}

	messages := []Message{
		{Headers: map[string]string{"traceparent": "invalid"}},
		{Headers: nil},
		{Headers: map[string]string{}},
	}

	links := ExtractLinksFromBatch(messages, func(m Message) map[string]string {
		return m.Headers
	})

	assert.Empty(t, links)
}

func TestExtractLinksFromBatch_MapMessages(t *testing.T) {
	// Test with map[string]any messages (common in JSON scenarios)
	messages := []map[string]any{
		{
			"body": "hello",
			"headers": map[string]string{
				"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			},
		},
		{
			"body": "world",
			"headers": map[string]string{
				"traceparent": "00-11111111111111111111111111111111-2222222222222222-01",
			},
		},
	}

	links := ExtractLinksFromBatch(messages, func(m map[string]any) map[string]string {
		if h, ok := m["headers"].(map[string]string); ok {
			return h
		}
		return nil
	})

	assert.Len(t, links, 2)
}

func TestCreateLinkFromContext(t *testing.T) {
	// Set up a tracer provider
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	tracer := otel.Tracer("test")

	// Create a span to link from
	ctx, span := tracer.Start(context.Background(), "producer-span")
	defer span.End()

	// Create a link from the context
	link := CreateLinkFromContext(ctx, attribute.String("link.type", "test"))

	assert.True(t, link.SpanContext.IsValid())
	assert.Equal(t, span.SpanContext().TraceID(), link.SpanContext.TraceID())
	assert.Equal(t, span.SpanContext().SpanID(), link.SpanContext.SpanID())
	assert.Len(t, link.Attributes, 1)
}

func TestCreateLinkFromContext_NoSpan(t *testing.T) {
	// Context with no span
	ctx := context.Background()

	link := CreateLinkFromContext(ctx)

	// Should return a link with invalid span context
	assert.False(t, link.SpanContext.IsValid())
}

// Integration test: Create link from headers and use with sampler
func TestLinksBasedSampling_Integration(t *testing.T) {
	// Simulate a producer span's trace context (sampled)
	producerHeaders := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}

	// Create link from producer headers
	link, ok := CreateLinkFromHeaders(producerHeaders)
	require.True(t, ok)

	// Create sampler with links-based enabled
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0), // No baseline sampling
		WithLinksBased(true),
		WithLinksRate(1.0),
	)

	// Consumer span should be sampled because it links to sampled producer
	result := sampler.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
		Links:         []trace.Link{link},
	})

	assert.Equal(t, sdktrace.RecordAndSample, result.Decision)
}
