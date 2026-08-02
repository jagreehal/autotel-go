package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/jagreehal/autotel-go/v2"
)

// GinMiddleware returns a Gin middleware that traces requests.
// It creates spans for each HTTP request and records standard HTTP attributes.
//
// Example:
//
//	r := gin.Default()
//	r.Use(middleware.GinMiddleware("my-service"))
//	r.GET("/users/:id", handleGetUser)
func GinMiddleware(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Create span name from method and path
		spanName := c.Request.Method + " " + c.FullPath()
		if spanName == " " {
			// Fallback if FullPath is empty (e.g., 404)
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := autotel.Start(c.Request.Context(), spanName)
		defer span.End()

		// Update request context
		c.Request = c.Request.WithContext(ctx)

		// Set HTTP attributes following OpenTelemetry semantic conventions
		span.SetAttribute("http.method", c.Request.Method)
		span.SetAttribute("http.url", c.Request.URL.String())
		span.SetAttribute("http.route", c.FullPath())
		span.SetAttribute("http.scheme", c.Request.URL.Scheme)
		span.SetAttribute("http.target", c.Request.URL.Path)

		// Set user agent if present
		if userAgent := c.Request.UserAgent(); userAgent != "" {
			span.SetAttribute("http.user_agent", userAgent)
		}

		// Set client IP if available
		if clientIP := c.ClientIP(); clientIP != "" {
			span.SetAttribute("net.sock.peer.addr", clientIP)
		}

		// Process request
		c.Next()

		// Record status code
		statusCode := c.Writer.Status()
		span.SetAttribute("http.status_code", statusCode)

		// Record errors if any
		if len(c.Errors) > 0 {
			lastErr := c.Errors.Last()
			span.RecordError(lastErr)
			// Add error details as attributes
			span.SetAttribute("error.type", fmt.Sprintf("%v", lastErr.Type))
			span.SetAttribute("error.message", lastErr.Error())
		}

		// Record response size if available
		if c.Writer.Size() > 0 {
			span.SetAttribute("http.response.body.size", c.Writer.Size())
		}
	}
}
