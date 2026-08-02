package autotel_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/subscribers"
)

func TestTrackFunnelStep(t *testing.T) {
	sub := subscribers.NewInMemorySubscriber()
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithSubscribers(sub),
	)
	require.NoError(t, err)
	defer cleanup()

	// Allow queue to start
	time.Sleep(10 * time.Millisecond)

	autotel.TrackFunnelStep(context.Background(), "checkout", autotel.FunnelStarted, map[string]any{
		"user_id": "123",
	})

	// Allow event to be processed
	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "funnel.checkout.started", events[0].Event)
	assert.Equal(t, "checkout", events[0].Properties["funnel_name"])
	assert.Equal(t, "started", events[0].Properties["funnel_status"])
	assert.Equal(t, "123", events[0].Properties["user_id"])
}

func TestTrackFunnelProgression(t *testing.T) {
	sub := subscribers.NewInMemorySubscriber()
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithSubscribers(sub),
	)
	require.NoError(t, err)
	defer cleanup()

	time.Sleep(10 * time.Millisecond)

	autotel.TrackFunnelProgression(context.Background(), "onboarding", "verify_email", 2, map[string]any{
		"user_id": "456",
	})

	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "funnel.onboarding.step", events[0].Event)
	assert.Equal(t, "onboarding", events[0].Properties["funnel_name"])
	assert.Equal(t, "verify_email", events[0].Properties["step_name"])
	assert.Equal(t, 2, events[0].Properties["step_number"])
	assert.Equal(t, "456", events[0].Properties["user_id"])
}

func TestTrackOutcome(t *testing.T) {
	sub := subscribers.NewInMemorySubscriber()
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithSubscribers(sub),
	)
	require.NoError(t, err)
	defer cleanup()

	time.Sleep(10 * time.Millisecond)

	autotel.TrackOutcome(context.Background(), "payment_processing", autotel.OutcomeSuccess, map[string]any{
		"amount": 99.99,
	})

	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "outcome.payment_processing", events[0].Event)
	assert.Equal(t, "payment_processing", events[0].Properties["operation_name"])
	assert.Equal(t, "success", events[0].Properties["outcome"])
	assert.Equal(t, 99.99, events[0].Properties["amount"])
}

func TestTrackValue(t *testing.T) {
	sub := subscribers.NewInMemorySubscriber()
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithSubscribers(sub),
	)
	require.NoError(t, err)
	defer cleanup()

	time.Sleep(10 * time.Millisecond)

	autotel.TrackValue(context.Background(), "order_total", 149.99, map[string]any{
		"currency": "USD",
	})

	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "value.order_total", events[0].Event)
	assert.Equal(t, 149.99, events[0].Properties["value"])
	assert.Equal(t, "USD", events[0].Properties["currency"])
}

func TestTrackBatch(t *testing.T) {
	sub := subscribers.NewInMemorySubscriber()
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithSubscribers(sub),
	)
	require.NoError(t, err)
	defer cleanup()

	time.Sleep(10 * time.Millisecond)

	autotel.TrackBatch(context.Background(), []autotel.Event{
		{Name: "page_view", Properties: map[string]any{"page": "/home"}},
		{Name: "button_click", Properties: map[string]any{"button": "signup"}},
	})

	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	require.Len(t, events, 2)
	assert.Equal(t, "page_view", events[0].Event)
	assert.Equal(t, "/home", events[0].Properties["page"])
	assert.Equal(t, "button_click", events[1].Event)
	assert.Equal(t, "signup", events[1].Properties["button"])
}

func TestTrackFunnelStep_NoTracker(t *testing.T) {
	// Init without subscribers
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
	)
	require.NoError(t, err)
	defer cleanup()

	// Should not panic when no tracker is configured
	autotel.TrackFunnelStep(context.Background(), "checkout", autotel.FunnelStarted, nil)
}

func TestFunnelStatus_Values(t *testing.T) {
	assert.Equal(t, autotel.FunnelStatus("started"), autotel.FunnelStarted)
	assert.Equal(t, autotel.FunnelStatus("completed"), autotel.FunnelCompleted)
	assert.Equal(t, autotel.FunnelStatus("abandoned"), autotel.FunnelAbandoned)
	assert.Equal(t, autotel.FunnelStatus("failed"), autotel.FunnelFailed)
}

func TestOutcomeStatus_Values(t *testing.T) {
	assert.Equal(t, autotel.OutcomeStatus("success"), autotel.OutcomeSuccess)
	assert.Equal(t, autotel.OutcomeStatus("failure"), autotel.OutcomeFailure)
	assert.Equal(t, autotel.OutcomeStatus("partial"), autotel.OutcomePartial)
}

func TestWithHeaders(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("test"),
		autotel.WithHeaders(map[string]string{
			"X-Custom-Header": "test-value",
		}),
	)
	require.NoError(t, err)
	defer cleanup()
}
