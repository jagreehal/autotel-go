package mcp

import (
	"context"
	"testing"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestStartClientSpan(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := StartClientSpan(ctx, MethodToolsCall, &ClientSpanOptions{
		ToolName:         "get_weather",
		NetworkTransport: "pipe",
	})
	defer span.End()

	if !span.IsRecording() {
		t.Error("expected recording span")
	}
	// Attributes are set in span; we can't read them back without exporter
	// Just ensure no panic and span is created
}

func TestStartClientSpan_NoOptions(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := StartClientSpan(ctx, MethodToolsList, nil)
	defer span.End()

	if !span.IsRecording() {
		t.Error("expected recording span")
	}
}

func TestStartServerSpan(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := StartServerSpan(ctx, MethodToolsCall, &ServerSpanOptions{
		ToolName:         "get_weather",
		NetworkTransport: "pipe",
	})
	defer span.End()

	if !span.IsRecording() {
		t.Error("expected recording span")
	}
}
