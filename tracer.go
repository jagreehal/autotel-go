package autotel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/jagreehal/autotel-go/v2"

type contextKey string

const (
	operationNameKey contextKey = "autotel.operation.name"
)

// GetOperationName returns the operation/span name stored on context by Start/Trace helpers.
func GetOperationName(ctx context.Context) string {
	name, _ := GetOperationContext(ctx)
	return name
}

// GetOperationContext returns the current operation name if set (e.g. by Start, Trace, or RunInOperationContext).
// Use this when you need to know whether an operation context is present (ok == true).
func GetOperationContext(ctx context.Context) (name string, ok bool) {
	if v, ok := ctx.Value(operationNameKey).(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// RunInOperationContext runs fn with the given operation name set on context.
// Events tracked inside fn will have operation.name set to name (when using the global event queue).
// Use this to attach an operation name to events without starting a span.
func RunInOperationContext[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error) {
	ctx = context.WithValue(ctx, operationNameKey, name)
	return fn(ctx)
}

var (
	globalPIIRedactor interface {
		Redact(key, value string) string
	}
	mu sync.RWMutex
)

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
//
// The rate limiter and circuit breaker are not consulted here. They used to be,
// which meant they only ever covered spans created through this function: spans
// from middleware/httpclient, messaging, workflow and every third-party
// instrumentation package went straight to otel.Tracer and skipped both, so a
// configured limit did not bound the span volume it appeared to bound. Both now
// run in the sampler, which every span passes through whatever created it. See
// guardSampler in autotel.go.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span) {
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
