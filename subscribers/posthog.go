package subscribers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PostHogSubscriber sends analytics events to PostHog.
type PostHogSubscriber struct {
	apiKey     string
	host       string
	client     *http.Client
	distinctID string
}

// NewPostHogSubscriber creates a new PostHog subscriber.
// apiKey is your PostHog API key (phc_...).
// host is the PostHog host (default: https://app.posthog.com).
func NewPostHogSubscriber(apiKey string, opts ...PostHogOption) *PostHogSubscriber {
	subscriber := &PostHogSubscriber{
		apiKey: apiKey,
		host:   "https://app.posthog.com",
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(subscriber)
	}

	return subscriber
}

// PostHogOption configures a PostHog subscriber.
type PostHogOption func(*PostHogSubscriber)

// WithPostHogHost sets a custom PostHog host.
func WithPostHogHost(host string) PostHogOption {
	return func(s *PostHogSubscriber) {
		s.host = host
	}
}

// WithPostHogDistinctID sets a default distinct ID for events.
func WithPostHogDistinctID(distinctID string) PostHogOption {
	return func(s *PostHogSubscriber) {
		s.distinctID = distinctID
	}
}

// WithPostHogTimeout sets the HTTP client timeout.
func WithPostHogTimeout(timeout time.Duration) PostHogOption {
	return func(s *PostHogSubscriber) {
		s.client.Timeout = timeout
	}
}

// Send sends an analytics event to PostHog.
// Properties should already contain trace_id and span_id if available.
func (s *PostHogSubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	// PostHog API format
	payload := map[string]any{
		"api_key":    s.apiKey,
		"event":      event,
		"properties": properties,
	}

	// Add distinct ID if set
	if s.distinctID != "" {
		payload["distinct_id"] = s.distinctID
	} else if properties != nil {
		// Try to extract from properties
		if userID, ok := properties["user_id"].(string); ok {
			payload["distinct_id"] = userID
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	url := fmt.Sprintf("%s/capture/", s.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// Close closes the subscriber.
func (s *PostHogSubscriber) Close() error {
	return nil
}
