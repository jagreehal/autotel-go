package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestEchoMiddleware_RoutePattern(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	e := echo.New()
	e.HideBanner = true
	e.Use(EchoMiddleware("test-service"))
	e.GET("/todos/:id", func(c echo.Context) error {
		return c.String(http.StatusOK, c.Param("id"))
	})

	req := httptest.NewRequest(http.MethodGet, "/todos/abc-123", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "abc-123", rr.Body.String())

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)

	span := spans[0]
	assert.Equal(t, "GET /todos/:id", span.Name())
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.SpanContext().TraceID().String())
	assert.Equal(t, "/todos/:id", spanAttrString(span, "http.route"))
}

func TestEchoMiddleware_HandlerError(t *testing.T) {
	exporter, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	e := echo.New()
	e.HideBanner = true
	e.Use(EchoMiddleware("test-service"))
	e.GET("/boom", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusBadGateway, "upstream")
	})

	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom", nil))
	require.Equal(t, http.StatusBadGateway, rr.Code)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)
	assert.Equal(t, "GET /boom", spans[0].Name())
}
