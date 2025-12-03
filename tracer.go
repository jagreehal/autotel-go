package autotel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/jagreehal/autotel-go"

type contextKey string

const (
	operationNameKey contextKey = "autotel.operation.name"
)

// GetOperationName returns the operation/span name stored on context by Start/Trace helpers.
func GetOperationName(ctx context.Context) string {
	if v, ok := ctx.Value(operationNameKey).(string); ok {
		return v
	}
	return ""
}

var (
	globalRateLimiter    interface{ Allow() bool }
	globalCircuitBreaker interface{ Allow() bool }
	globalPIIRedactor    interface {
		Redact(key, value string) string
	}
	mu sync.RWMutex
)

// setGlobalRateLimiter sets the global rate limiter (internal use)
func setGlobalRateLimiter(rl interface{ Allow() bool }) {
	mu.Lock()
	defer mu.Unlock()
	globalRateLimiter = rl
}

// setGlobalCircuitBreaker sets the global circuit breaker (internal use)
func setGlobalCircuitBreaker(cb interface{ Allow() bool }) {
	mu.Lock()
	defer mu.Unlock()
	globalCircuitBreaker = cb
}

// setGlobalPIIRedactor sets the global PII redactor (internal use)
func setGlobalPIIRedactor(pr interface {
	Redact(key, value string) string
}) {
	mu.Lock()
	defer mu.Unlock()
	globalPIIRedactor = pr
}

// Start creates a new span and returns the updated context and span.
// The span should be ended with span.End() or defer span.End().
//
// Example:
//
//	func CreateUser(ctx context.Context, data UserData) error {
//	    ctx, span := autotel.Start(ctx, "CreateUser")
//	    defer span.End()
//
//	    span.SetAttribute("user.email", data.Email)
//	    return db.Users.Create(ctx, data)
//	}
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span) {
	// Check rate limiter
	mu.RLock()
	rl := globalRateLimiter
	mu.RUnlock()
	if rl != nil && !rl.Allow() {
		// Rate limited - return non-recording span
		debugPrint("⚠ Rate limited: %s", name)
		return ctx, &noopSpan{}
	}

	// Check circuit breaker
	mu.RLock()
	cb := globalCircuitBreaker
	mu.RUnlock()
	if cb != nil && !cb.Allow() {
		// Circuit breaker open - return non-recording span
		debugPrint("⚠ Circuit breaker open: %s", name)
		return ctx, &noopSpan{}
	}

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, name, opts...)
	ctx = context.WithValue(ctx, operationNameKey, name)

	// Debug logging
	if span.IsRecording() {
		debugSpanStart(span.SpanContext(), name)
	}

	return ctx, &spanImpl{span: span}
}

// Trace wraps a function with automatic span lifecycle management.
// The function receives the updated context and span.
// If the function returns an error, it's automatically recorded.
//
// Example:
//
//	func GetUser(ctx context.Context, id string) (*User, error) {
//	    return autotel.Trace(ctx, "GetUser", func(ctx context.Context, span autotel.Span) (*User, error) {
//	        span.SetAttribute("user.id", id)
//	        return db.Users.FindByID(ctx, id)
//	    })
//	}
func Trace[T any](ctx context.Context, name string, fn func(context.Context, Span) (T, error)) (T, error) {
	ctx, span := Start(ctx, name)
	defer span.End()

	result, err := fn(ctx, span)
	if err != nil {
		span.RecordError(err)
	}

	return result, err
}

// TraceNoError is like Trace but for functions that don't return errors.
func TraceNoError[T any](ctx context.Context, name string, fn func(context.Context, Span) T) T {
	ctx, span := Start(ctx, name)
	defer span.End()

	return fn(ctx, span)
}

// TraceVoid is like Trace but for functions that don't return a value.
func TraceVoid(ctx context.Context, name string, fn func(context.Context, Span) error) error {
	ctx, span := Start(ctx, name)
	defer span.End()

	err := fn(ctx, span)
	if err != nil {
		span.RecordError(err)
	}

	return err
}
