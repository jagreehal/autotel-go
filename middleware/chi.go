package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/riandyrn/otelchi"
)

// ChiMiddleware wraps [otelchi.Middleware] — the standard OpenTelemetry
// instrumentation for go-chi/chi. It joins W3C trace context and records
// http.route from Chi's matched pattern.
//
// Prefer [ChiMiddlewareOn] when you have the router so the span is named from
// the route template at creation time.
//
// Example:
//
//	r := chi.NewRouter()
//	r.Use(middleware.ChiMiddlewareOn(r, "my-service"))
func ChiMiddleware(serviceName string, opts ...otelchi.Option) func(http.Handler) http.Handler {
	defaults := []otelchi.Option{otelchi.WithRequestMethodInSpanName(true)}
	return otelchi.Middleware(serviceName, append(defaults, opts...)...)
}

// ChiMiddlewareOn is [ChiMiddleware] with [otelchi.WithChiRoutes] so span names
// use the route template (e.g. GET /todos/{id}) from the start of the request.
func ChiMiddlewareOn(router chi.Routes, serviceName string, opts ...otelchi.Option) func(http.Handler) http.Handler {
	opts = append([]otelchi.Option{otelchi.WithChiRoutes(router)}, opts...)
	return ChiMiddleware(serviceName, opts...)
}
