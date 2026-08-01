package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/redaction"
	"github.com/jagreehal/autotel-go/v2/sampling"
)

func main() {
	// Initialize autotel with production hardening features
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("production-service"),
		autotel.WithServiceVersion("1.0.0"),
		autotel.WithEnvironment("production"),
		autotel.WithEndpoint("http://localhost:4318"),

		// Production hardening
		autotel.WithAdaptiveSampler(
			sampling.WithBaselineRate(0.1), // 10% baseline sampling
			sampling.WithErrorRate(1.0),    // 100% error sampling
		),
		autotel.WithRateLimit(100, 200),                  // 100 spans/sec, burst of 200
		autotel.WithCircuitBreaker(5, 3, 30*time.Second), // 5 failures, 3 successes, 30s timeout
		autotel.WithPIIRedaction(
			redaction.WithAllowlistKeys("user_id", "order_id"), // Allow these keys
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create HTTP server with tracing middleware
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/users", handleUsers)

	handler := middleware.HTTPMiddleware("production-service")(mux)

	log.Println("Starting production-ready server on :8080")
	log.Println("Features enabled:")
	log.Println("  - Adaptive sampling (10% baseline, 100% errors)")
	log.Println("  - Rate limiting (100 spans/sec)")
	log.Println("  - Circuit breaker protection")
	log.Println("  - PII redaction")

	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	_, span := autotel.Start(r.Context(), "handleUsers")
	defer span.End()

	// These will be redacted (PII)
	span.SetAttribute("user.email", "user@example.com")
	span.SetAttribute("user.phone", "555-123-4567")

	// These will NOT be redacted (allowlisted)
	span.SetAttribute("user_id", "user-123")
	span.SetAttribute("order_id", "order-456")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`[{"id":"user-123","name":"Alice"}]`))
}
