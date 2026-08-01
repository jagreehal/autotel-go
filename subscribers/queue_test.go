package subscribers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/jagreehal/autotel-go/v2"
	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

// testSubscriber is a simple subscriber for testing that stores events in memory.
type testSubscriber struct {
	mu     sync.RWMutex
	events []testEvent
}

type testEvent struct {
	name       string
	properties map[string]any
}

func newTestSubscriber() *testSubscriber {
	return &testSubscriber{
		events: make([]testEvent, 0),
	}
}

func (s *testSubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, testEvent{
		name:       event,
		properties: properties,
	})
	return nil
}

func (s *testSubscriber) Close() error {
	return nil
}

func (s *testSubscriber) getEvents() []testEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]testEvent, len(s.events))
	copy(events, s.events)
	return events
}

func TestQueue_Track(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	sub := newTestSubscriber()
	queue := NewQueue(sub)
	defer func() { _ = queue.Shutdown(context.Background()) }()

	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "test-operation")
	defer span.End()

	queue.Track(ctx, "test_event", map[string]any{
		"key": "value",
	})

	// Give time for async processing
	time.Sleep(50 * time.Millisecond)

	events := sub.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].name)
	assert.Equal(t, "value", events[0].properties["key"])
	assert.NotEmpty(t, events[0].properties["trace_id"])
	assert.NotEmpty(t, events[0].properties["span_id"])
}

func TestQueue_Track_NoSpan(t *testing.T) {
	sub := newTestSubscriber()
	queue := NewQueue(sub)
	defer func() { _ = queue.Shutdown(context.Background()) }()

	ctx := context.Background()
	queue.Track(ctx, "test_event", map[string]any{"key": "value"})

	time.Sleep(50 * time.Millisecond)

	events := sub.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].name)
	// trace_id and span_id should not be present when no span
	_, hasTraceID := events[0].properties["trace_id"]
	_, hasSpanID := events[0].properties["span_id"]
	assert.False(t, hasTraceID)
	assert.False(t, hasSpanID)
}

func TestQueue_Shutdown(t *testing.T) {
	sub := newTestSubscriber()
	queue := NewQueue(sub)

	ctx := context.Background()
	queue.Track(ctx, "event1", nil)
	queue.Track(ctx, "event2", nil)

	// Shutdown should flush pending events
	err := queue.Shutdown(context.Background())
	assert.NoError(t, err)

	events := sub.getEvents()
	assert.GreaterOrEqual(t, len(events), 2)
}

func TestQueue_MultipleSubscribers(t *testing.T) {
	sub1 := newTestSubscriber()
	sub2 := newTestSubscriber()
	queue := NewQueue(sub1, sub2)
	defer func() { _ = queue.Shutdown(context.Background()) }()

	ctx := context.Background()
	queue.Track(ctx, "test_event", map[string]any{"key": "value"})

	time.Sleep(50 * time.Millisecond)

	events1 := sub1.getEvents()
	events2 := sub2.getEvents()

	assert.Len(t, events1, 1)
	assert.Len(t, events2, 1)
	assert.Equal(t, events1[0].name, events2[0].name)
}

func TestQueue_UsesInMemorySubscriber(t *testing.T) {
	sub := NewInMemorySubscriber()
	queue := NewQueue(sub)
	defer func() { _ = queue.Shutdown(context.Background()) }()

	ctx := context.Background()
	queue.Track(ctx, "test_event", map[string]any{"key": "value"})

	time.Sleep(50 * time.Millisecond)

	events := sub.GetEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].Event)
	assert.Equal(t, "value", events[0].Properties["key"])
}
