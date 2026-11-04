package middleware

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type httpConfig struct {
	skipPaths         []string
	responseHeader    bool
	trustForwardedFor bool
}

// HTTPOption configures HTTPMiddleware.
type HTTPOption func(*httpConfig)

// WithSkipPaths records nothing for requests to these exact paths: spans or
// metrics. Use it for health checks and other probes.
func WithSkipPaths(paths ...string) HTTPOption {
	return func(c *httpConfig) {
		c.skipPaths = append(c.skipPaths, paths...)
	}
}

// WithTraceResponseHeader answers with a traceparent header, so a caller can
// quote the trace of a failed request. It exposes only the trace and span ID,
// never baggage.
func WithTraceResponseHeader() HTTPOption {
	return func(c *httpConfig) {
		c.responseHeader = true
	}
}

// WithTrustForwardedFor records client.address from X-Forwarded-For. Any client
// can send that header, so use this only behind a proxy that overwrites it.
// Without it, client.address is the TCP peer.
func WithTrustForwardedFor() HTTPOption {
	return func(c *httpConfig) {
		c.trustForwardedFor = true
	}
}

// HTTPMiddleware returns an HTTP middleware that traces requests with otelhttp.
//
// Span names stay low-cardinality: "GET /orders/{id}" once an http.ServeMux has
// matched a route, and plain "GET" otherwise. A raw path would create a new span
// name per order ID. For the route to be seen, this must wrap the mux with no
// middleware between them that replaces the request (r.WithContext does).
//
// Example:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /users/{id}", handleUser)
//
//	handler := middleware.HTTPMiddleware("my-service",
//		middleware.WithSkipPaths("/healthz"),
//	)(mux)
//	http.ListenAndServe(":8080", handler)
func HTTPMiddleware(serviceName string, opts ...HTTPOption) func(http.Handler) http.Handler {
	cfg := &httpConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	otelOpts := []otelhttp.Option{
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return spanName(r)
		}),
	}
	if len(cfg.skipPaths) > 0 {
		otelOpts = append(otelOpts, otelhttp.WithFilter(func(r *http.Request) bool {
			return !slices.Contains(cfg.skipPaths, r.URL.Path)
		}))
	}

	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(instrumented(next, cfg), serviceName, otelOpts...)
	}
}

// instrumented runs inside the otelhttp span and adds what it can't know at the
// start of a request.
func instrumented(next http.Handler, cfg *httpConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())

		if !cfg.trustForwardedFor && r.RemoteAddr != "" {
			span.SetAttributes(attribute.String("client.address", peerAddress(r.RemoteAddr)))
		}
		if cfg.responseHeader {
			setTraceparent(r, w)
		}

		// ServeMux sets r.Pattern on this request while routing it. otelhttp
		// renames the span after a normal return, through spanName; this also
		// covers a panic, which skips everything otelhttp does afterwards.
		returned := false
		defer func() {
			if route := routeFromPattern(r.Pattern); route != "" {
				span.SetName(spanName(r))
				span.SetAttributes(attribute.String("http.route", route))
			}
			if !returned {
				span.SetStatus(codes.Error, "panic in handler")
			}
		}()
		next.ServeHTTP(w, r)
		returned = true
	})
}

// peerAddress drops the port. A unix socket's address has none, and is kept.
func peerAddress(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}

// setTraceparent copies only traceparent: the propagator also writes tracestate,
// which came from the caller and isn't ours to reflect.
func setTraceparent(r *http.Request, w http.ResponseWriter) {
	carrier := propagation.HeaderCarrier{}
	propagation.TraceContext{}.Inject(r.Context(), carrier)
	if value := carrier.Get("traceparent"); value != "" {
		w.Header().Set("traceparent", value)
	}
}

// spanName is "METHOD /route" once a route is known, and "METHOD" before that.
func spanName(r *http.Request) string {
	if route := routeFromPattern(r.Pattern); route != "" {
		return spanMethod(r.Method) + " " + route
	}
	return spanMethod(r.Method)
}

// routeFromPattern takes the path from a ServeMux pattern such as
// "GET example.com/orders/{id}".
func routeFromPattern(pattern string) string {
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = strings.TrimLeft(rest, " \t")
	}
	if i := strings.IndexByte(pattern, '/'); i >= 0 {
		return pattern[i:]
	}
	return ""
}

// spanMethod keeps made-up methods out of span names, as the HTTP conventions ask.
func spanMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	case "":
		return http.MethodGet
	}
	return "HTTP"
}

// HTTPMiddlewareWithOptions returns an HTTP middleware with custom otelhttp
// options and nothing else: span naming and client.address are otelhttp's, so
// the path goes in the span name only if your formatter puts it there, and
// X-Forwarded-For is trusted. Prefer HTTPMiddleware unless you need an option
// it doesn't offer.
//
// Example:
//
//	handler := middleware.HTTPMiddlewareWithOptions("my-service",
//		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
//			return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
//		}),
//	)(mux)
func HTTPMiddlewareWithOptions(serviceName string, opts ...otelhttp.Option) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, serviceName, opts...)
	}
}
