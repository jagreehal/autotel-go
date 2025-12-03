package subscribers

import (
	"context"
	"sync"
)

// InMemoryEvent represents an event stored in memory.
type InMemoryEvent struct {
	Event      string
	Properties map[string]any
}

// InMemorySubscriber stores analytics events in memory (useful for testing).
type InMemorySubscriber struct {
	mu     sync.RWMutex
	events []InMemoryEvent
}

// NewInMemorySubscriber creates a new in-memory subscriber.
func NewInMemorySubscriber() *InMemorySubscriber {
	return &InMemorySubscriber{
		events: make([]InMemoryEvent, 0),
	}
}

// Send stores an event in memory.
func (s *InMemorySubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, InMemoryEvent{
		Event:      event,
		Properties: properties,
	})
	return nil
}

// GetEvents returns all stored events.
func (s *InMemorySubscriber) GetEvents() []InMemoryEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]InMemoryEvent, len(s.events))
	copy(events, s.events)
	return events
}

// Reset clears all stored events.
func (s *InMemorySubscriber) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = make([]InMemoryEvent, 0)
}

// Close closes the subscriber.
func (s *InMemorySubscriber) Close() error {
	return nil
}
