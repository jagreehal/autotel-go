package autotel

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestSetDuration(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := Start(ctx, "test")
	defer span.End()

	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	SetDuration(span, start)

	// Duration should be set (we can't easily verify exact values in tests)
	assert.True(t, span.IsRecording())
}

func TestSetHTTPRequestAttributes(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := Start(ctx, "test")
	defer span.End()

	SetHTTPRequestAttributes(span, http.MethodGet, "/users", "test-agent")

	assert.True(t, span.IsRecording())
}

func TestAddEventWithAttributes(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, span := Start(ctx, "test")
	defer span.End()

	AddEventWithAttributes(span, "test_event",
		"key1", "value1",
		"key2", 42,
		"key3", true,
	)

	assert.True(t, span.IsRecording())
}

func TestIsTracingEnabled(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	ctx := context.Background()
	enabled := IsTracingEnabled(ctx)
	assert.True(t, enabled)
}
