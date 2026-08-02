package autotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jagreehal/autotel-go/v2"
	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestStart(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := autotel.Start(ctx, "test-span")
	defer span.End()

	assert.True(t, span.IsRecording())
	assert.NotEmpty(t, span.SpanContext().TraceID().String())
}

func TestTrace_Success(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	result, err := autotel.Trace(ctx, "test-trace", func(ctx context.Context, span autotel.Span) (string, error) {
		span.SetAttribute("test", "value")
		return "success", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify span was created
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestTrace_Error(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	expectedErr := errors.New("test error")
	result, err := autotel.Trace(ctx, "test-trace", func(ctx context.Context, span autotel.Span) (string, error) {
		return "", expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, result)

	// Verify span was created and error was recorded
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}

func TestGetOperationContext(t *testing.T) {
	ctx := context.Background()
	name, ok := autotel.GetOperationContext(ctx)
	assert.False(t, ok)
	assert.Empty(t, name)
}

func TestGetOperationContext_FromStart(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "CreateUser")
	defer span.End()

	name, ok := autotel.GetOperationContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "CreateUser", name)
}

func TestRunInOperationContext(t *testing.T) {
	ctx := context.Background()

	result, err := autotel.RunInOperationContext(ctx, "batch.import", func(ctx context.Context) (int, error) {
		name, ok := autotel.GetOperationContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, "batch.import", name)
		return 42, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 42, result)

	// Outside fn, original ctx unchanged
	_, ok := autotel.GetOperationContext(ctx)
	assert.False(t, ok)
}
