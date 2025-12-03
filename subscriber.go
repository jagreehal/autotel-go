package autotel

import "context"

// Subscriber is implemented by analytics subscribers (PostHog, Mixpanel, etc.).
// Defined at root to keep autotel API typed without import cycles.
type Subscriber interface {
	Send(ctx context.Context, event string, properties map[string]any) error
	Close() error
}
