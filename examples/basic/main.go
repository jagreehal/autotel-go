package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jagreehal/autotel-go"
)

func main() {
	// Initialize autotel with debug mode
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("basic-example"),
		autotel.WithDebug(true), // Enable verbose console logging
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Example 1: Using Start() with defer
	ctx := context.Background()
	ctx, span := autotel.Start(ctx, "ProcessOrder")
	defer span.End()

	span.SetAttribute("order.id", "12345")
	span.SetAttribute("order.amount", 99.99)

	// Example 2: Using Trace() helper
	result, err := autotel.Trace(ctx, "GetUser", func(ctx context.Context, span autotel.Span) (string, error) {
		span.SetAttribute("user.id", "user-123")
		return "user-data", nil
	})
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		fmt.Printf("Result: %s\n", result)
	}

	fmt.Println("Example completed successfully!")
}
