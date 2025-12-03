package main

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jagreehal/autotel-go"
	"github.com/jagreehal/autotel-go/middleware"
)

func main() {
	// Initialize autotel
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("gin-server-example"),
		autotel.WithEndpoint("http://localhost:4318"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	// Create Gin router
	r := gin.Default()

	// Add tracing middleware
	r.Use(middleware.GinMiddleware("gin-server-example"))

	// Define routes
	r.GET("/", handleRoot)
	r.GET("/users/:id", handleGetUser)
	r.POST("/users", handleCreateUser)

	// Start server
	log.Println("Starting Gin server on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}

func handleRoot(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Hello, World!"})
}

func handleGetUser(c *gin.Context) {
	_, span := autotel.Start(c.Request.Context(), "getUser")
	defer span.End()

	id := c.Param("id")
	span.SetAttribute("user.id", id)

	c.JSON(http.StatusOK, gin.H{"id": id, "name": "Alice"})
}

func handleCreateUser(c *gin.Context) {
	_, span := autotel.Start(c.Request.Context(), "createUser")
	defer span.End()

	var user struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&user); err != nil {
		span.RecordError(err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	span.SetAttribute("user.name", user.Name)
	c.JSON(http.StatusCreated, gin.H{"id": 1, "name": user.Name})
}
