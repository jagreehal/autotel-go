package subscribers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// MixpanelSubscriber sends analytics events to Mixpanel.
type MixpanelSubscriber struct {
	token      string
	apiSecret  string
	host       string
	client     *http.Client
	distinctID string
}

// NewMixpanelSubscriber creates a new Mixpanel subscriber.
// token is your Mixpanel project token.
func NewMixpanelSubscriber(token string, opts ...MixpanelOption) *MixpanelSubscriber {
	subscriber := &MixpanelSubscriber{
		token: token,
		host:  "https://api.mixpanel.com",
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(subscriber)
	}

	return subscriber
}

// MixpanelOption configures a Mixpanel subscriber.
type MixpanelOption func(*MixpanelSubscriber)

// WithMixpanelHost sets a custom Mixpanel host.
func WithMixpanelHost(host string) MixpanelOption {
	return func(s *MixpanelSubscriber) {
		s.host = host
	}
}

// WithMixpanelAPISecret sets the API secret for server-side tracking.
func WithMixpanelAPISecret(apiSecret string) MixpanelOption {
	return func(s *MixpanelSubscriber) {
		s.apiSecret = apiSecret
	}
}

// WithMixpanelDistinctID sets a default distinct ID for events.
func WithMixpanelDistinctID(distinctID string) MixpanelOption {
	return func(s *MixpanelSubscriber) {
		s.distinctID = distinctID
	}
}

// WithMixpanelTimeout sets the HTTP client timeout.
func WithMixpanelTimeout(timeout time.Duration) MixpanelOption {
	return func(s *MixpanelSubscriber) {
		s.client.Timeout = timeout
	}
}

// Send sends an analytics event to Mixpanel.
// Properties should already contain trace_id and span_id if available.
func (s *MixpanelSubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	// Mixpanel event format
	// Initialize properties map
	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}

	eventData := map[string]any{
		"event":      event,
		"properties": props,
	}

	// Add token to properties
	props["token"] = s.token

	// Add distinct ID
	if s.distinctID != "" {
		props["distinct_id"] = s.distinctID
	} else if properties != nil {
		if userID, ok := properties["user_id"].(string); ok {
			props["distinct_id"] = userID
		}
	}

	// Encode event data
	eventJSON, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Base64 encode for Mixpanel API
	encoded := base64.URLEncoding.EncodeToString(eventJSON)

	// Create request
	apiURL := fmt.Sprintf("%s/track/", s.host)
	formData := url.Values{}
	formData.Set("data", encoded)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBufferString(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
func (s *MixpanelSubscriber) Close() error {
	return nil
}
