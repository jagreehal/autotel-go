package subscribers

import "context"

// Subscriber sends analytics events to a destination.
// This is the Go equivalent of Python's EventSubscriber protocol.
//
// Subscribers receive product events and forward them to external platforms
// like PostHog, Mixpanel, Amplitude, webhooks, or custom destinations.
type Subscriber interface {
	// Send sends an analytics event to the destination.
	// The event name and properties are passed separately for better ergonomics.
	Send(ctx context.Context, event string, properties map[string]any) error

	// Close closes the subscriber and releases any resources.
	// This is called during queue shutdown to clean up connections, etc.
	Close() error
}
