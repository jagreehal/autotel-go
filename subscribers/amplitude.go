package subscribers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AmplitudeSubscriber sends analytics events to Amplitude.
type AmplitudeSubscriber struct {
	apiKey   string
	host     string
	client   *http.Client
	userID   string
	deviceID string
}

// NewAmplitudeSubscriber creates a new Amplitude subscriber.
// apiKey is your Amplitude API key.
func NewAmplitudeSubscriber(apiKey string, opts ...AmplitudeOption) *AmplitudeSubscriber {
	subscriber := &AmplitudeSubscriber{
		apiKey: apiKey,
		host:   "https://api2.amplitude.com",
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(subscriber)
	}

	return subscriber
}

// AmplitudeOption configures an Amplitude subscriber.
type AmplitudeOption func(*AmplitudeSubscriber)

// WithAmplitudeHost sets a custom Amplitude host.
func WithAmplitudeHost(host string) AmplitudeOption {
	return func(s *AmplitudeSubscriber) {
		s.host = host
	}
}

// WithAmplitudeUserID sets a default user ID for events.
func WithAmplitudeUserID(userID string) AmplitudeOption {
	return func(s *AmplitudeSubscriber) {
		s.userID = userID
	}
}

// WithAmplitudeDeviceID sets a default device ID for events.
func WithAmplitudeDeviceID(deviceID string) AmplitudeOption {
	return func(s *AmplitudeSubscriber) {
		s.deviceID = deviceID
	}
}

// WithAmplitudeTimeout sets the HTTP client timeout.
func WithAmplitudeTimeout(timeout time.Duration) AmplitudeOption {
	return func(s *AmplitudeSubscriber) {
		s.client.Timeout = timeout
	}
}

// Send sends an analytics event to Amplitude.
// Properties should already contain trace_id and span_id if available.
func (s *AmplitudeSubscriber) Send(ctx context.Context, event string, properties map[string]any) error {
	// Amplitude event format
	eventData := map[string]any{
		"event_type":       event,
		"event_properties": properties,
	}

	// Add user ID
	if s.userID != "" {
		eventData["user_id"] = s.userID
	} else if properties != nil {
		if userID, ok := properties["user_id"].(string); ok {
			eventData["user_id"] = userID
		}
	}

	// Add device ID
	if s.deviceID != "" {
		eventData["device_id"] = s.deviceID
	}

	// Amplitude API format
	payload := map[string]any{
		"api_key": s.apiKey,
		"events":  []map[string]any{eventData},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	url := fmt.Sprintf("%s/2/httpapi", s.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")

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
func (s *AmplitudeSubscriber) Close() error {
	return nil
}
