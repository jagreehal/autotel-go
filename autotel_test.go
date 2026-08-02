package autotel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jagreehal/autotel-go/v2"
)

func TestInit_Basic(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test-service"),
	)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	defer cleanup()

	// Verify tracer provider is set
	// (Check global otel.GetTracerProvider())
	// Note: We can't directly access GetTracerProvider without exposing it
	// This test verifies Init() completes without error
}

func TestInit_WithCustomEndpoint(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithEndpoint("custom:4318"),
	)
	require.NoError(t, err)
	defer cleanup()
}

func TestInit_WithGRPCProtocol(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithProtocol(autotel.ProtocolGRPC),
	)
	require.NoError(t, err)
	defer cleanup()
}
