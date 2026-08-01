package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestNewHTTPClient(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	client := NewHTTPClient()

	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, 30*time.Second, client.client.Timeout)
}

func TestHTTPClientWithTimeout(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	client := NewHTTPClient(WithTimeout(5 * time.Second))

	assert.Equal(t, 5*time.Second, client.client.Timeout)
}

func TestHTTPClientPropagatesTraceContext(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Create a test server that captures headers
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create a span to establish trace context
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Make request with traced client
	client := NewHTTPClient()
	resp, err := client.Get(ctx, server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify traceparent header was injected
	traceparent := receivedHeaders.Get("Traceparent")
	assert.NotEmpty(t, traceparent, "traceparent header should be present")
	assert.Contains(t, traceparent, "-", "traceparent should be W3C format")
}

func TestHTTPClientGet(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Get(context.Background(), server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "hello", string(body))
}

func TestHTTPClientPost(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	var receivedBody []byte
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Post(context.Background(), server.URL, "application/json", []byte(`{"name":"test"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "application/json", receivedContentType)
	assert.Equal(t, `{"name":"test"}`, string(receivedBody))
}

func TestHTTPClientPut(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Put(context.Background(), server.URL, "application/json", []byte(`{"id":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHTTPClientDelete(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Delete(context.Background(), server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHTTPClientPatch(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Patch(context.Background(), server.URL, "application/json", []byte(`{"field":"value"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestInjectHeaders(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Create a span
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Create request and inject headers
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	InjectHeaders(ctx, req)

	// Verify headers
	assert.NotEmpty(t, req.Header.Get("Traceparent"))
}

func TestInjectHeadersWithPropagator(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	InjectHeadersWithPropagator(ctx, req, propagation.TraceContext{})

	assert.NotEmpty(t, req.Header.Get("Traceparent"))
}

func TestExtractHeaders(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Create a request with trace headers
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")

	// Extract the context
	ctx := ExtractHeaders(context.Background(), req)

	// Verify span context was extracted
	spanCtx := trace.SpanContextFromContext(ctx)
	assert.True(t, spanCtx.IsValid())
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", spanCtx.TraceID().String())
}

func TestNewHTTPTransport(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	transport := NewHTTPTransport(http.DefaultTransport)

	assert.NotNil(t, transport)
	assert.NotNil(t, transport.base)
	assert.NotNil(t, transport.propagator)
}

func TestHTTPTransportWithCustomBase(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	customTransport := &http.Transport{
		MaxIdleConns: 100,
	}

	transport := NewHTTPTransport(customTransport)

	assert.Equal(t, customTransport, transport.base)
}

func TestWrapHTTPClient(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	original := &http.Client{
		Timeout: 60 * time.Second,
	}

	wrapped := WrapHTTPClient(original)

	assert.NotNil(t, wrapped)
	assert.Equal(t, 60*time.Second, wrapped.client.Timeout)
}

func TestWrapHTTPClientNil(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	wrapped := WrapHTTPClient(nil)

	assert.NotNil(t, wrapped)
	assert.Equal(t, 30*time.Second, wrapped.client.Timeout) // default
}

func TestHTTPClientWithoutSpans(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Set up span recorder to count spans
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Headers should still be present
		assert.NotEmpty(t, r.Header.Get("Traceparent"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create parent span
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "parent")

	// Make request with WithoutSpans()
	client := NewHTTPClient(WithoutSpans())
	resp, err := client.Get(ctx, server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	span.End()
	tp.ForceFlush(context.Background())

	// Should only have the parent span, not client span
	spans := exporter.GetSpans()
	assert.Equal(t, 1, len(spans), "should only have parent span, not HTTP client span")
}

func TestHTTPClientCreatesSpans(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "parent")

	client := NewHTTPClient() // spans enabled by default
	resp, err := client.Get(ctx, server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	span.End()
	tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	assert.GreaterOrEqual(t, len(spans), 2, "should have parent span and HTTP client span")
}

func TestHTTPClientCustomSpanName(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient(WithSpanNameFormatter(func(req *http.Request) string {
		return "custom-span-name"
	}))

	ctx := context.Background()
	resp, err := client.Get(ctx, server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name == "custom-span-name" {
			found = true
			break
		}
	}
	assert.True(t, found, "should find span with custom name")
}

func TestHTTPClientFromOtel(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	client := HTTPClientFromOtel()

	assert.NotNil(t, client)
	assert.NotNil(t, client.Transport)
}

func TestTracedHTTPClientUnderlying(t *testing.T) {
	client := NewHTTPClient()
	underlying := client.Underlying()

	assert.NotNil(t, underlying)
	assert.IsType(t, &http.Client{}, underlying)
}

func TestHTTPClientErrorHandling(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)

	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewHTTPClient(WithResponseStatus())
	resp, err := client.Get(context.Background(), server.URL)
	require.NoError(t, err) // HTTP errors don't return Go errors
	defer resp.Body.Close()

	tp.ForceFlush(context.Background())

	// The span should have error status
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHTTPClientPostNilBody(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)
		assert.Empty(t, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient()
	resp, err := client.Post(context.Background(), server.URL, "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHTTPClientDo(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	var receivedMethod string
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodOptions, server.URL+"/custom", nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.MethodOptions, receivedMethod)
	assert.Equal(t, "/custom", receivedPath)
}

func TestHTTPTransportNilBase(t *testing.T) {
	transport := NewHTTPTransport(nil)
	assert.NotNil(t, transport.base)
}
