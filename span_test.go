package autotel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go"
	autoteltesting "github.com/jagreehal/autotel-go/testing"
)

func TestSpan_SetAttribute(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span")
	span.SetAttribute("string", "value")
	span.SetAttribute("int", 42)
	span.SetAttribute("int64", int64(100))
	span.SetAttribute("float64", 3.14)
	span.SetAttribute("bool", true)
	span.End()

	// SimpleSpanProcessor exports synchronously, so spans should be available immediately
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestSpan_AddEvent(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span")
	span.AddEvent("test-event", attribute.String("key", "value"))
	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestSpan_SetStatus(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span")
	span.SetStatus(codes.Ok, "success")
	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestSpan_RecordError(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span")
	err := assert.AnError
	span.RecordError(err)
	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestSpan_SetAttribute_ArrayTypes(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span-arrays")

	// Test all array types
	span.SetAttribute("string_slice", []string{"a", "b", "c"})
	span.SetAttribute("int_slice", []int{1, 2, 3})
	span.SetAttribute("int64_slice", []int64{100, 200, 300})
	span.SetAttribute("float64_slice", []float64{1.1, 2.2, 3.3})
	span.SetAttribute("bool_slice", []bool{true, false, true})

	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)

	// Verify attributes were set
	if len(spans) > 0 {
		attrs := spans[0].Attributes
		assert.NotEmpty(t, attrs, "span should have attributes")
	}
}

func TestSpan_SetAttribute_MixedTypes(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span-mixed")

	// Mix of primitives and arrays
	span.SetAttribute("name", "test")
	span.SetAttribute("count", 42)
	span.SetAttribute("tags", []string{"tag1", "tag2"})
	span.SetAttribute("scores", []float64{95.5, 87.3})

	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestSpan_UpdateName(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "original-name")
	span.UpdateName("updated-name")
	span.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
	if len(spans) > 0 {
		assert.Equal(t, "updated-name", spans[0].Name())
	}
}

func TestSpan_AddLink(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create first span to link to
	_, span1 := autotel.Start(ctx, "span-to-link")
	linkedSpanContext := span1.SpanContext()
	span1.End()

	// Create second span and add link
	_, span2 := autotel.Start(ctx, "span-with-link")
	span2.AddLink(trace.Link{SpanContext: linkedSpanContext})
	span2.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 2)

	// The link is recorded as an event (due to OTel Go SDK limitation)
	if len(spans) >= 2 {
		events := spans[1].Events
		assert.NotEmpty(t, events, "span should have link event")
	}
}

func TestSpan_AddLinks(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create spans to link to
	ctx1, span1 := autotel.Start(ctx, "linked-span-1")
	sc1 := span1.SpanContext()
	span1.End()

	_, span2 := autotel.Start(ctx1, "linked-span-2")
	sc2 := span2.SpanContext()
	span2.End()

	// Create span with multiple links
	_, span3 := autotel.Start(ctx, "span-with-links")
	span3.AddLinks(
		trace.Link{SpanContext: sc1},
		trace.Link{SpanContext: sc2},
	)
	span3.End()

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 3)
}
