package main

import (
	"context"
	"log"
	"time"

	"github.com/jagreehal/autotel-go"
	"github.com/jagreehal/autotel-go/subscribers"
)

func main() {
	// Initialize autotel
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("analytics-posthog-example"),
		autotel.WithEndpoint("http://localhost:4318"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create event queue with PostHog subscriber
	// Replace with your actual PostHog API key
	queue := subscribers.NewQueue(
		subscribers.NewPostHogSubscriber("your-posthog-api-key",
			subscribers.WithPostHogDistinctID("user-123"),
		),
	)
	defer queue.Shutdown(context.Background())

	// Track user signup event
	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "userSignup")
	defer span.End()

	span.SetAttribute("user.id", "user-123")
	span.SetAttribute("user.email", "user@example.com")

	// Track analytics event (automatically enriched with trace context)
	queue.Track(ctx, "user_signed_up", map[string]any{
		"user_id":     "user-123",
		"email":       "user@example.com",
		"plan":        "premium",
		"signup_date": time.Now().Format(time.RFC3339),
	})

	// Track another event
	queue.Track(ctx, "subscription_created", map[string]any{
		"subscription_id": "sub-456",
		"plan":            "premium",
		"amount":          29.99,
	})

	// Give time for async processing
	time.Sleep(200 * time.Millisecond)

	log.Println("Analytics events sent to PostHog!")
	log.Println("Check your PostHog dashboard to see events with trace context")
}
