package main

import (
	"context"
	"log"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/subscribers"
)

func main() {
	// Initialize autotel with debug mode
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("analytics-example"),
		autotel.WithEndpoint("http://localhost:4318"),
		autotel.WithDebug(true), // Enable debug mode
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create event queue with in-memory subscriber (for testing)
	// In production, use: subscribers.NewWebhookSubscriber("https://api.posthog.com", ...)
	sub := subscribers.NewInMemorySubscriber()
	queue := subscribers.NewQueue(sub)
	defer queue.Shutdown(context.Background())

	// Example: Track user signup event
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

	// Give time for async processing
	time.Sleep(100 * time.Millisecond)

	// In production, events would be sent to webhook
	// For this example, we can check in-memory subscriber
	events := sub.GetEvents()
	log.Printf("Tracked %d analytics events", len(events))
	for _, event := range events {
		traceID := ""
		spanID := ""
		if tid, ok := event.Properties["trace_id"].(string); ok {
			traceID = tid
		}
		if sid, ok := event.Properties["span_id"].(string); ok {
			spanID = sid
		}
		log.Printf("Event: %s, TraceID: %s, SpanID: %s", event.Event, traceID, spanID)
	}

	log.Println("Example completed successfully!")
}
