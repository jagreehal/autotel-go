package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
)

func main() {
	// Initialize autotel
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("http-server-example"),
		autotel.WithEndpoint("http://localhost:4318"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/users", handleUsers)

	// Wrap with tracing middleware
	handler := middleware.HTTPMiddleware("http-server-example")(mux)

	// Start server
	log.Println("Starting HTTP server on :8080")
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

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Hello, World!"))
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	_, span := autotel.Start(r.Context(), "handleUsers")
	defer span.End()

	span.SetAttribute("http.method", r.Method)
	span.SetAttribute("user.action", "list")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`[{"id": 1, "name": "Alice"}]`))
}
