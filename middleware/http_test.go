package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/codes"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
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

func serveMux(t *testing.T, opts ...HTTPOption) (*autoteltesting.InMemoryExporter, http.Handler) {
	t.Helper()
	exporter, cleanup := autoteltesting.SetupTest(t)
	t.Cleanup(cleanup)
	mux := http.NewServeMux()
	mux.HandleFunc("GET example.com/orders/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("/healthz", func(http.ResponseWriter, *http.Request) {})
	return exporter, HTTPMiddleware("orders", opts...)(mux)
}

func attrOf(t *testing.T, exporter *autoteltesting.InMemoryExporter, key string) (string, bool) {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	for _, kv := range spans[0].Attributes() {
		if string(kv.Key) == key {
			return kv.Value.String(), true
		}
	}
	return "", false
}

func TestSpanNameUsesTheRouteNotThePath(t *testing.T) {
	exporter, handler := serveMux(t)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/orders/12345?token=s3cret", nil))

	spans := exporter.GetSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, "GET /orders/{id}", spans[0].Name())
	route, _ := attrOf(t, exporter, "http.route")
	assert.Equal(t, "/orders/{id}", route)
	for _, kv := range spans[0].Attributes() {
		assert.NotContains(t, kv.Value.String(), "s3cret", kv.Key)
	}
}

func TestUnmatchedRequestsShareOneSpanName(t *testing.T) {
	exporter, handler := serveMux(t)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/random/123", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/random/456", nil))

	spans := exporter.GetSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, "GET", spans[0].Name())
	assert.Equal(t, "HTTP", spans[1].Name())
}

func TestPanickingHandlerKeepsItsRoute(t *testing.T) {
	exporter, handler := serveMux(t)
	assert.Panics(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	})
	span := exporter.GetSpans()[0]
	assert.Equal(t, "GET /boom", span.Name())
	assert.Equal(t, codes.Error, span.Status().Code)
}

func TestClientAddressIsThePeerUnlessTrusted(t *testing.T) {
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://example.com/orders/1", nil)
		r.RemoteAddr = "10.0.0.7:5555"
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
		return r
	}

	exporter, handler := serveMux(t)
	handler.ServeHTTP(httptest.NewRecorder(), request())
	address, _ := attrOf(t, exporter, "client.address")
	assert.Equal(t, "10.0.0.7", address)

	exporter, handler = serveMux(t, WithTrustForwardedFor())
	handler.ServeHTTP(httptest.NewRecorder(), request())
	address, _ = attrOf(t, exporter, "client.address")
	assert.Equal(t, "9.9.9.9", address)
}

func TestUnixSocketPeerDoesNotFallBackToForwardedFor(t *testing.T) {
	exporter, handler := serveMux(t)
	r := httptest.NewRequest(http.MethodGet, "/orders/1", nil)
	r.RemoteAddr = "@"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	address, _ := attrOf(t, exporter, "client.address")
	assert.Equal(t, "@", address)
}

func TestWithSkipPaths(t *testing.T) {
	exporter, handler := serveMux(t, WithSkipPaths("/healthz"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, exporter.GetSpans())
}

func TestWithTraceResponseHeader(t *testing.T) {
	exporter, handler := serveMux(t, WithTraceResponseHeader())
	r := httptest.NewRequest(http.MethodGet, "http://example.com/orders/1", nil)
	r.Header.Set("baggage", "secret=1")
	r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	r.Header.Set("tracestate", "vendor=secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	assert.Contains(t, rr.Header().Get("traceparent"), exporter.GetSpans()[0].SpanContext().TraceID().String())
	assert.Empty(t, rr.Header().Get("baggage"))
	assert.Empty(t, rr.Header().Get("tracestate"), "the caller's tracestate isn't ours to reflect")
}

func TestNoTraceHeaderByDefault(t *testing.T) {
	_, handler := serveMux(t)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/orders/1", nil))
	assert.Empty(t, rr.Header().Get("traceparent"))
}

func TestRouteFromPattern(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"/orders":                    "/orders",
		"GET /orders/{id}":           "/orders/{id}",
		"GET  example.com/x/{id...}": "/x/{id...}",
		"example.com/":               "/",
	} {
		assert.Equal(t, want, routeFromPattern(in), in)
	}
}
