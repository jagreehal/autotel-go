package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// GinMiddleware wraps [otelgin.Middleware] — the official OpenTelemetry
// contrib instrumentation for Gin. It joins W3C trace context and records
// http.route from Gin's FullPath.
//
// Example:
//
//	r := gin.Default()
//	r.Use(middleware.GinMiddleware("my-service"))
//	r.GET("/users/:id", handleGetUser)
func GinMiddleware(serviceName string, opts ...otelgin.Option) gin.HandlerFunc {
	return otelgin.Middleware(serviceName, opts...)
}
