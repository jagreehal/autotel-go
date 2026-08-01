package middleware

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// HTTPClientConfig holds configuration for the traced HTTP client.
type HTTPClientConfig struct {
	// Timeout for HTTP requests (default: 30s)
	Timeout time.Duration

	// BaseTransport is the underlying transport (default: http.DefaultTransport)
	BaseTransport http.RoundTripper

	// Propagator for trace context injection (default: W3C TraceContext + Baggage)
	Propagator propagation.TextMapPropagator

	// SpanNameFormatter customizes span names (default: "HTTP {method}")
	SpanNameFormatter func(req *http.Request) string

	// DisableSpanCreation skips span creation but still propagates context
	DisableSpanCreation bool

	// RecordResponseBody records the response status in span attributes
	RecordResponseStatus bool
}

// HTTPClientOption configures the traced HTTP client.
type HTTPClientOption func(*HTTPClientConfig)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.Timeout = timeout
	}
}

// WithBaseTransport sets the underlying HTTP transport.
func WithBaseTransport(transport http.RoundTripper) HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.BaseTransport = transport
	}
}

// WithPropagator sets a custom propagator for trace context.
func WithPropagator(propagator propagation.TextMapPropagator) HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.Propagator = propagator
	}
}

// WithSpanNameFormatter customizes how span names are generated.
//
// Example:
//
//	client := middleware.NewHTTPClient(
//	    middleware.WithSpanNameFormatter(func(req *http.Request) string {
//	        return fmt.Sprintf("%s %s", req.Method, req.URL.Path)
//	    }),
//	)
func WithSpanNameFormatter(formatter func(req *http.Request) string) HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.SpanNameFormatter = formatter
	}
}

// WithoutSpans disables span creation but still propagates trace context.
// Useful when you want header propagation without creating spans for every request.
func WithoutSpans() HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.DisableSpanCreation = true
	}
}

// WithResponseStatus enables recording HTTP response status in span attributes.
func WithResponseStatus() HTTPClientOption {
	return func(c *HTTPClientConfig) {
		c.RecordResponseStatus = true
	}
}

// defaultHTTPClientConfig returns the default configuration.
func defaultHTTPClientConfig() *HTTPClientConfig {
	return &HTTPClientConfig{
		Timeout:       30 * time.Second,
		BaseTransport: http.DefaultTransport,
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		SpanNameFormatter: func(req *http.Request) string {
			return "HTTP " + req.Method
		},
		RecordResponseStatus: true,
	}
}

// NewHTTPClient creates an HTTP client with automatic trace context propagation.
// It injects W3C traceparent/tracestate headers into all outbound requests,
// enabling distributed tracing across service boundaries.
//
// Example:
//
//	// Basic usage - just works!
//	client := middleware.NewHTTPClient()
//	resp, err := client.Get(ctx, "https://api.example.com/users")
//
//	// With options
//	client := middleware.NewHTTPClient(
//	    middleware.WithTimeout(10 * time.Second),
//	    middleware.WithSpanNameFormatter(func(req *http.Request) string {
//	        return fmt.Sprintf("call %s", req.URL.Host)
//	    }),
//	)
func NewHTTPClient(opts ...HTTPClientOption) *TracedHTTPClient {
	cfg := defaultHTTPClientConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return &TracedHTTPClient{
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: NewHTTPTransport(cfg.BaseTransport, opts...),
		},
		config: cfg,
	}
}

// TracedHTTPClient wraps http.Client with automatic trace propagation.
type TracedHTTPClient struct {
	client *http.Client
	config *HTTPClientConfig
}

// Do executes an HTTP request with trace context propagation.
// The context's trace span is automatically propagated via W3C headers.
func (c *TracedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

// Get performs a GET request with trace context propagation.
func (c *TracedHTTPClient) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post performs a POST request with trace context propagation.
func (c *TracedHTTPClient) Post(ctx context.Context, url, contentType string, body []byte) (*http.Response, error) {
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// Put performs a PUT request with trace context propagation.
func (c *TracedHTTPClient) Put(ctx context.Context, url, contentType string, body []byte) (*http.Response, error) {
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// Delete performs a DELETE request with trace context propagation.
func (c *TracedHTTPClient) Delete(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Patch performs a PATCH request with trace context propagation.
func (c *TracedHTTPClient) Patch(ctx context.Context, url, contentType string, body []byte) (*http.Response, error) {
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPatch, url, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// Underlying returns the wrapped *http.Client for advanced use cases.
func (c *TracedHTTPClient) Underlying() *http.Client {
	return c.client
}

// TracedTransport is an http.RoundTripper that propagates trace context.
type TracedTransport struct {
	base              http.RoundTripper
	propagator        propagation.TextMapPropagator
	spanNameFormatter func(req *http.Request) string
	disableSpans      bool
	recordStatus      bool
}

// NewHTTPTransport creates a traced RoundTripper that wraps an existing transport.
// Use this when you need to customize the underlying transport or integrate
// with existing HTTP clients.
//
// Example:
//
//	// Wrap a custom transport
//	transport := &http.Transport{
//	    MaxIdleConns: 100,
//	    IdleConnTimeout: 90 * time.Second,
//	}
//	tracedTransport := middleware.NewHTTPTransport(transport)
//
//	client := &http.Client{Transport: tracedTransport}
func NewHTTPTransport(base http.RoundTripper, opts ...HTTPClientOption) *TracedTransport {
	cfg := defaultHTTPClientConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	if base == nil {
		base = http.DefaultTransport
	}

	return &TracedTransport{
		base:              base,
		propagator:        cfg.Propagator,
		spanNameFormatter: cfg.SpanNameFormatter,
		disableSpans:      cfg.DisableSpanCreation,
		recordStatus:      cfg.RecordResponseStatus,
	}
}

// RoundTrip implements http.RoundTripper with trace context propagation.
//
// Per the http.RoundTripper contract the incoming request is never modified:
// headers are injected into a clone, so retries and redirects that reuse the
// original request are unaffected.
func (t *TracedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	if t.disableSpans {
		outbound := req.Clone(ctx)
		t.propagator.Inject(ctx, propagation.HeaderCarrier(outbound.Header))
		return t.base.RoundTrip(outbound)
	}

	// Create a span for this outbound request
	tracer := otel.Tracer("autotel/httpclient")
	spanName := t.spanNameFormatter(req)
	ctx, span := tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(requestAttributes(req)...),
	)
	defer span.End()

	// Clone onto the span context so the injected headers carry this span.
	outbound := req.Clone(ctx)
	t.propagator.Inject(ctx, propagation.HeaderCarrier(outbound.Header))

	// Execute the request
	resp, err := t.base.RoundTrip(outbound)

	// Record response status
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return resp, err
	}

	if t.recordStatus && resp != nil {
		span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
		if resp.StatusCode >= 400 {
			span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
		}
	}

	return resp, err
}

// requestAttributes returns the OTel semantic-convention attributes for an
// outbound client request. The URL is recorded without user info or query
// string, which routinely carry credentials and tokens.
func requestAttributes(req *http.Request) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(req.Method),
	}

	if req.URL == nil {
		return attrs
	}

	redacted := *req.URL
	redacted.User = nil
	redacted.RawQuery = ""
	redacted.Fragment = ""
	attrs = append(attrs, semconv.URLFull(redacted.String()))

	if req.URL.Path != "" {
		attrs = append(attrs, semconv.URLPath(req.URL.Path))
	}
	if req.URL.Scheme != "" {
		attrs = append(attrs, semconv.URLScheme(req.URL.Scheme))
	}
	if host := req.URL.Hostname(); host != "" {
		attrs = append(attrs, semconv.ServerAddress(host))
	}
	if portStr := req.URL.Port(); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			attrs = append(attrs, semconv.ServerPort(port))
		}
	}

	return attrs
}

// InjectHeaders injects W3C trace context headers into an HTTP request.
// Use this when you have an existing request and want to add trace headers
// without wrapping your HTTP client.
//
// Example:
//
//	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
//	middleware.InjectHeaders(ctx, req)
//	resp, err := http.DefaultClient.Do(req)
func InjectHeaders(ctx context.Context, req *http.Request) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// InjectHeadersWithPropagator injects trace context using a custom propagator.
//
// Example:
//
//	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
//	propagator := propagation.TraceContext{} // W3C only, no baggage
//	middleware.InjectHeadersWithPropagator(ctx, req, propagator)
func InjectHeadersWithPropagator(ctx context.Context, req *http.Request, propagator propagation.TextMapPropagator) {
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// ExtractHeaders extracts trace context from incoming HTTP request headers.
// Useful for manual context extraction in custom middleware.
//
// Example:
//
//	func myHandler(w http.ResponseWriter, r *http.Request) {
//	    ctx := middleware.ExtractHeaders(r.Context(), r)
//	    // ctx now contains the trace context from the request
//	}
func ExtractHeaders(ctx context.Context, req *http.Request) context.Context {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	return propagator.Extract(ctx, propagation.HeaderCarrier(req.Header))
}

// WrapHTTPClient wraps an existing *http.Client with trace propagation.
// The original client's Transport is wrapped, preserving timeouts and other settings.
//
// Example:
//
//	existingClient := &http.Client{Timeout: 60 * time.Second}
//	tracedClient := middleware.WrapHTTPClient(existingClient)
func WrapHTTPClient(client *http.Client, opts ...HTTPClientOption) *TracedHTTPClient {
	if client == nil {
		return NewHTTPClient(opts...)
	}

	cfg := defaultHTTPClientConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	tracedTransport := &TracedTransport{
		base:              transport,
		propagator:        cfg.Propagator,
		spanNameFormatter: cfg.SpanNameFormatter,
		disableSpans:      cfg.DisableSpanCreation,
		recordStatus:      cfg.RecordResponseStatus,
	}

	return &TracedHTTPClient{
		client: &http.Client{
			Transport:     tracedTransport,
			CheckRedirect: client.CheckRedirect,
			Jar:           client.Jar,
			Timeout:       client.Timeout,
		},
		config: cfg,
	}
}

// HTTPClientFromOtel creates a traced client using OpenTelemetry's otelhttp directly.
// This provides full otelhttp features including metrics and detailed span attributes.
//
// Example:
//
//	client := middleware.HTTPClientFromOtel()
func HTTPClientFromOtel(opts ...otelhttp.Option) *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport, opts...),
	}
}
