package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func TestGinMiddleware(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create Gin router
	r := gin.New()
	r.Use(GinMiddleware("test-service"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	// Execute request
	r.ServeHTTP(w, req)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGinMiddleware_WithError(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(GinMiddleware("test-service"))
	r.GET("/error", func(c *gin.Context) {
		_ = c.Error(assert.AnError)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	})

	req := httptest.NewRequest(http.MethodGet, "/error", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGinMiddleware_WithParams(t *testing.T) {
	_, cleanup := autoteltesting.SetupTest(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(GinMiddleware("test-service"))
	r.GET("/users/:id", func(c *gin.Context) {
		id := c.Param("id")
		c.JSON(http.StatusOK, gin.H{"id": id})
	})

	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
