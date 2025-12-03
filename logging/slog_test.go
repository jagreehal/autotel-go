package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jagreehal/autotel-go"
	autoteltesting "github.com/jagreehal/autotel-go/testing"
)

func TestWithTraceContext(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "test-operation")
	defer span.End()

	attrs := WithTraceContext(ctx)
	assert.Len(t, attrs, 2)

	// Check that trace_id and span_id are present
	traceIDFound := false
	spanIDFound := false
	for _, attr := range attrs {
		if attr.Key == "trace_id" {
			traceIDFound = true
			assert.NotEmpty(t, attr.Value.String())
		}
		if attr.Key == "span_id" {
			spanIDFound = true
			assert.NotEmpty(t, attr.Value.String())
		}
	}
	assert.True(t, traceIDFound)
	assert.True(t, spanIDFound)
}

func TestWithTraceContext_NoSpan(t *testing.T) {
	ctx := context.Background()
	attrs := WithTraceContext(ctx)
	assert.Nil(t, attrs)
}

func TestTraceHandler(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, nil)
	traceHandler := NewTraceHandler(baseHandler)
	logger := slog.New(traceHandler)

	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "test-operation")
	defer span.End()

	logger.InfoContext(ctx, "test message", slog.String("key", "value"))

	// Parse JSON output
	var logData map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logData)
	assert.NoError(t, err)

	// Verify trace context is present
	assert.Contains(t, logData, "trace_id")
	assert.Contains(t, logData, "span_id")
	assert.Equal(t, "test message", logData["msg"])
	assert.Equal(t, "value", logData["key"])
}

func TestTraceHandler_NoSpan(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, nil)
	traceHandler := NewTraceHandler(baseHandler)
	logger := slog.New(traceHandler)

	ctx := context.Background()
	logger.InfoContext(ctx, "test message")

	var logData map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logData)
	assert.NoError(t, err)

	// Trace context should not be present
	assert.NotContains(t, logData, "trace_id")
	assert.NotContains(t, logData, "span_id")
}

func TestTraceHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, nil)
	traceHandler := NewTraceHandler(baseHandler)
	logger := traceHandler.WithAttrs([]slog.Attr{slog.String("service", "test")})
	finalLogger := slog.New(logger)

	ctx := context.Background()
	finalLogger.InfoContext(ctx, "test")

	var logData map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logData)
	assert.NoError(t, err)
	assert.Equal(t, "test", logData["service"])
}

func TestTraceHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, nil)
	traceHandler := NewTraceHandler(baseHandler)
	logger := traceHandler.WithGroup("group")
	finalLogger := slog.New(logger)

	ctx := context.Background()
	finalLogger.InfoContext(ctx, "test", slog.String("key", "value"))

	var logData map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logData)
	assert.NoError(t, err)
	// Group should be present
	assert.Contains(t, logData, "group")
}
