package subscribers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInMemorySubscriber_Send(t *testing.T) {
	sub := NewInMemorySubscriber()
	defer sub.Close()

	err := sub.Send(context.Background(), "test_event", map[string]any{"key": "value"})
	assert.NoError(t, err)

	events := sub.GetEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].Event)
	assert.Equal(t, "value", events[0].Properties["key"])
}

func TestInMemorySubscriber_Reset(t *testing.T) {
	sub := NewInMemorySubscriber()
	defer sub.Close()

	_ = sub.Send(context.Background(), "test", nil)

	assert.Len(t, sub.GetEvents(), 1)

	sub.Reset()
	assert.Len(t, sub.GetEvents(), 0)
}

func TestInMemorySubscriber_MultipleEvents(t *testing.T) {
	sub := NewInMemorySubscriber()
	defer sub.Close()

	_ = sub.Send(context.Background(), "event1", nil)
	_ = sub.Send(context.Background(), "event2", nil)
	_ = sub.Send(context.Background(), "event3", nil)

	events := sub.GetEvents()
	assert.Len(t, events, 3)
	assert.Equal(t, "event1", events[0].Event)
	assert.Equal(t, "event2", events[1].Event)
	assert.Equal(t, "event3", events[2].Event)
}
