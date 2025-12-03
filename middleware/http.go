package middleware

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPMiddleware returns an HTTP middleware that traces requests.
// It uses OpenTelemetry's otelhttp package for standard HTTP instrumentation.
//
// Example:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/users", handleUsers)
//
//	handler := middleware.HTTPMiddleware("my-service")(mux)
//	http.ListenAndServe(":8080", handler)
func HTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Use OpenTelemetry's otelhttp for standard HTTP instrumentation
		return otelhttp.NewHandler(next, serviceName,
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
			}),
		)
	}
}

// HTTPMiddlewareWithOptions returns an HTTP middleware with custom options.
// This allows for more advanced configuration of the HTTP instrumentation.
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
