// Example: Service-to-Service Tracing
//
// This example demonstrates distributed tracing between two services:
// - Service A (port 8080): Receives requests and calls Service B
// - Service B (port 8081): Downstream API service
//
// The trace context is automatically propagated via W3C traceparent headers.
//
// Run with:
//
//	AUTOTEL_DEBUG=true go run main.go
//
// Then test with:
//
//	curl http://localhost:8080/orders/123
//
// You'll see trace IDs flow through both services in the debug output.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
)

func main() {
	ctx := context.Background()

	// Initialize autotel once for both services (in production, each service
	// would have its own initialization)
	cleanup, err := autotel.Init(ctx,
		autotel.WithService("service-to-service-demo"),
		autotel.WithDebug(true),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create a traced HTTP client for service-to-service calls
	httpClient := middleware.NewHTTPClient(
		middleware.WithTimeout(10*time.Second),
		middleware.WithSpanNameFormatter(func(req *http.Request) string {
			return fmt.Sprintf("HTTP %s %s", req.Method, req.URL.Path)
		}),
	)

	// Start both services
	var wg sync.WaitGroup

	// Service B: Downstream API (users service)
	serviceBMux := http.NewServeMux()
	serviceBMux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx, span := autotel.Start(ctx, "GetUserDetails")
		defer span.End()

		// Simulate some work
		time.Sleep(50 * time.Millisecond)

		userID := r.URL.Path[len("/users/"):]
		span.SetAttribute("user.id", userID)

		// Return user data
		user := map[string]any{
			"id":    userID,
			"name":  "John Doe",
			"email": "john@example.com",
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(user); err != nil {
			log.Printf("encode user response: %v", err)
		}
	})

	serviceBHandler := middleware.HTTPMiddleware("service-b")(serviceBMux)
	serverB := &http.Server{
		Addr:              ":8081",
		Handler:           serviceBHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Service B (Users API) starting on :8081")
		if err := serverB.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Service B error: %v", err)
		}
	}()

	// Service A: Gateway API (orders service)
	serviceAMux := http.NewServeMux()
	serviceAMux.HandleFunc("/orders/", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx, span := autotel.Start(ctx, "GetOrder")
		defer span.End()

		orderID := r.URL.Path[len("/orders/"):]
		span.SetAttribute("order.id", orderID)

		// Call Service B to get user details - trace context is automatically propagated!
		resp, err := httpClient.Get(ctx, "http://localhost:8081/users/user-456")
		if err != nil {
			span.RecordError(err)
			http.Error(w, "Failed to get user", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var user map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
			span.RecordError(err)
			http.Error(w, "Failed to decode user", http.StatusInternalServerError)
			return
		}

		// Combine order and user data
		order := map[string]any{
			"id":     orderID,
			"status": "completed",
			"total":  99.99,
			"user":   user,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(order); err != nil {
			log.Printf("encode order response: %v", err)
		}
	})

	// Health check endpoint
	serviceAMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	serviceAHandler := middleware.HTTPMiddleware("service-a")(serviceAMux)
	serverA := &http.Server{
		Addr:              ":8080",
		Handler:           serviceAHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Service A (Orders API) starting on :8080")
		if err := serverA.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Service A error: %v", err)
		}
	}()

	// Wait for services to start
	time.Sleep(100 * time.Millisecond)
	log.Println("")
	log.Println("Both services are running!")
	log.Println("Try: curl http://localhost:8080/orders/123")
	log.Println("")
	log.Println("Watch the debug output - you'll see the same trace_id flow through both services.")
	log.Println("Press Ctrl+C to stop.")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := serverA.Shutdown(ctx); err != nil {
		log.Printf("shut down service A: %v", err)
	}
	if err := serverB.Shutdown(ctx); err != nil {
		log.Printf("shut down service B: %v", err)
	}
	wg.Wait()
	log.Println("Shutdown complete")
}
