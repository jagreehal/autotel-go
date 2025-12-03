package subscribers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookSubscriber sends analytics events to a webhook endpoint.
// Useful for integrating with Zapier, Make.com, custom APIs, or any webhook endpoint.
type WebhookSubscriber struct {
	url     string
	client  *http.Client
	headers map[string]string
}

// NewWebhookSubscriber creates a new webhook subscriber.
func NewWebhookSubscriber(url string, opts ...WebhookOption) *WebhookSubscriber {
	subscriber := &WebhookSubscriber{
		url: url,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		headers: make(map[string]string),
	}

	for _, opt := range opts {
		opt(subscriber)
	}

	return subscriber
}

// WebhookOption configures a webhook subscriber.
type WebhookOption func(*WebhookSubscriber)

// WithWebhookHeaders sets custom HTTP headers for webhook requests.
func WithWebhookHeaders(headers map[string]string) WebhookOption {
	return func(s *WebhookSubscriber) {
		for k, v := range headers {
			s.headers[k] = v
		}
	}
}

// WithWebhookTimeout sets the HTTP client timeout.
func WithWebhookTimeout(timeout time.Duration) WebhookOption {
	return func(s *WebhookSubscriber) {
		s.client.Timeout = timeout
	}
}

// Send sends an analytics event to the webhook endpoint.
func (s *WebhookSubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	payload := map[string]any{
		"event":      event,
		"properties": properties,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

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
func (s *WebhookSubscriber) Close() error {
	// No resources to clean up
	return nil
}
