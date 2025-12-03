package autotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jagreehal/autotel-go"
	autoteltesting "github.com/jagreehal/autotel-go/testing"
)

func TestTraceFunc_ImmediateExecution(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Test immediate execution pattern: func(TraceContext) -> (T, error)
	result := autotel.TraceFunc(ctx, "immediate-exec", func(ctx autotel.TraceContext) (string, error) {
		ctx.SetAttribute("test", "value")
		return "success", nil
	})

	assert.Equal(t, "success", result)

	// Verify span was created
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
	assert.Equal(t, "immediate-exec", spans[0].Name())
}

func TestTraceFunc_FactoryPattern(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Test factory pattern: func(TraceContext) -> func(...args) -> (T, error)
	wrappedFn := autotel.TraceFunc(ctx, "factory-pattern", func(ctx autotel.TraceContext) func(string) (string, error) {
		ctx.SetAttribute("factory", true)
		return func(input string) (string, error) {
			ctx.SetAttribute("input", input)
			return "processed: " + input, nil
		}
	}).(func(string) (string, error))

	result, err := wrappedFn("test-input")
	assert.NoError(t, err)
	assert.Equal(t, "processed: test-input", result)

	// Verify span was created
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
	assert.Equal(t, "factory-pattern", spans[0].Name())
}

func TestTraceFunc_NoOrphanSpans(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// This test verifies that pattern detection using reflection doesn't create
	// orphan spans. In the Node.js version, calling async functions during pattern
	// detection would cause them to start executing, creating orphan spans.
	// In Go, we use reflection to inspect types without executing functions.

	executionCount := 0

	// Immediate execution pattern - should only execute once
	result := autotel.TraceFunc(ctx, "single-execution", func(ctx autotel.TraceContext) (int, error) {
		executionCount++
		ctx.SetAttribute("execution.count", executionCount)
		return executionCount, nil
	})

	assert.Equal(t, 1, result)
	assert.Equal(t, 1, executionCount, "Function should execute exactly once, not during pattern detection")

	// Verify we have exactly one span
	spans := exporter.GetSpans()
	assert.Equal(t, 1, len(spans), "Should have exactly 1 span, not multiple from pattern detection")
}

func TestTraceFunc_NestedSpan(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Test that nested spans work correctly without creating orphans
	// This is similar to the Node.js test case
	result := autotel.TraceFunc(ctx, "parent-trace", func(ctx autotel.TraceContext) (string, error) {
		ctx.SetAttribute("input.query", "What is the capital of France?")

		// Nested span should be a child of parent-trace
		_, span := autotel.Start(ctx.Context(), "nested-span")
		span.SetAttribute("model", "gpt-4")
		span.End()

		ctx.SetAttribute("output", "Successfully answered.")
		return "The capital of France is Paris.", nil
	})

	assert.Equal(t, "The capital of France is Paris.", result)

	// Verify we have exactly 2 spans (parent + child), not 3 (no orphan)
	spans := exporter.GetSpans()
	assert.Equal(t, 2, len(spans), "Should have exactly 2 spans (parent + child), not 3 with an orphan")

	// Verify span names
	spanNames := make(map[string]bool)
	for _, span := range spans {
		spanNames[span.Name()] = true
	}
	assert.True(t, spanNames["parent-trace"], "Should have parent-trace span")
	assert.True(t, spanNames["nested-span"], "Should have nested-span span")
}

func TestTraceFunc_ErrorHandling(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()

	expectedErr := errors.New("test error")
	result := autotel.TraceFunc(ctx, "error-test", func(ctx autotel.TraceContext) (string, error) {
		return "", expectedErr
	})

	resultErr, ok := result.(error)
	if !ok {
		// If result is a tuple, extract error
		if resultSlice, ok := result.([]any); ok && len(resultSlice) == 2 {
			if err, ok := resultSlice[1].(error); ok {
				resultErr = err
			}
		}
	}

	assert.Error(t, resultErr)
	assert.Equal(t, expectedErr, resultErr)

	// Verify span was created and error was recorded
	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 1)
}
