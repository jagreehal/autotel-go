package middleware

import (
	echootel "github.com/labstack/echo-otel/v5"
	"github.com/labstack/echo/v5"
)

// EchoMiddleware wraps LabStack's [echootel.NewMiddleware] — the maintained
// OpenTelemetry instrumentation for Echo v5 (replaces the deprecated
// contrib otelecho package).
//
// Example:
//
//	e := echo.New()
//	e.Use(middleware.EchoMiddleware("my-service"))
func EchoMiddleware(serviceName string) echo.MiddlewareFunc {
	return echootel.NewMiddleware(serviceName)
}

// EchoMiddlewareWithConfig wraps [echootel.NewMiddlewareWithConfig].
func EchoMiddlewareWithConfig(cfg echootel.Config) echo.MiddlewareFunc {
	return echootel.NewMiddlewareWithConfig(cfg)
}
