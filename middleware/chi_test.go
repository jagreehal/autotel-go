package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestChiMiddleware_RoutePattern(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	r := chi.NewRouter()
	r.Use(ChiMiddlewareOn(r, "test-service"))
	r.Get("/todos/{id}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(chi.URLParam(req, "id")))
	})

	req := httptest.NewRequest(http.MethodGet, "/todos/abc-123", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "abc-123", rr.Body.String())

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)

	span := spans[0]
	assert.Equal(t, "GET /todos/{id}", span.Name())
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.SpanContext().TraceID().String())
	assert.Equal(t, "/todos/{id}", spanAttrString(span, "http.route"))
	assert.Equal(t, int64(200), spanAttrInt(span, "http.status_code"))
}

func TestChiMiddleware_NotFound(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	r := chi.NewRouter()
	r.Use(ChiMiddlewareOn(r, "test-service"))
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/missing", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)
	assert.Equal(t, int64(404), spanAttrInt(spans[0], "http.status_code"))
}

func spanAttrString(span sdktrace.ReadOnlySpan, key string) string {
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

func spanAttrInt(span sdktrace.ReadOnlySpan, key string) int64 {
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsInt64()
		}
	}
	return 0
}
