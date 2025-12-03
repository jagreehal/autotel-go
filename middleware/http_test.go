package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	autoteltesting "github.com/jagreehal/autotel-go/testing"
)

func TestHTTPMiddleware(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Wrap with middleware
	middleware := HTTPMiddleware("test-service")
	wrappedHandler := middleware(handler)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	// Execute request
	wrappedHandler.ServeHTTP(rr, req)

	// Verify response
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestHTTPMiddlewareWithOptions(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPMiddlewareWithOptions("test-service")
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}
