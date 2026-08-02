// Package correlationid provides correlation ID utilities for event-driven observability.
// It gives a stable join key across events, logs, and spans even when traces fragment.
// Format: 16 hex chars (64 bits), crypto-random.
//
// Lifecycle:
//  1. Generated at boundary root (HTTP server span, message process span, cron job span)
//  2. Reused within context (nested work shares it via context)
//  3. Propagated via baggage (optional, so it flows across service boundaries)
package correlationid

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// Baggage key for correlation ID propagation.
const CORRELATION_ID_BAGGAGE_KEY = "autotel.correlation_id"

type contextKey struct{}

var correlationIDKey = contextKey{}

// GenerateCorrelationID returns a new correlation ID (16 hex chars, 64 bits, crypto-random).
func GenerateCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetOrCreateCorrelationID returns the current correlation ID from context, or generates a new one if none exists.
// It does not store the generated ID on context; use SetCorrelationID or RunWithCorrelationID to attach it.
func GetOrCreateCorrelationID(ctx context.Context) string {
	if id := GetCorrelationID(ctx); id != "" {
		return id
	}
	return GenerateCorrelationID()
}

// GetCorrelationID returns the current correlation ID from context.
// Resolution order: 1) context value (from SetCorrelationID or RunWithCorrelationID),
// 2) baggage (if propagated from upstream), 3) active span trace ID first 16 chars, 4) empty string.
func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok && id != "" {
		return id
	}
	bag := baggage.FromContext(ctx)
	if v := bag.Member(CORRELATION_ID_BAGGAGE_KEY).Value(); v != "" {
		return v
	}
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		tid := span.SpanContext().TraceID().String()
		if len(tid) >= 16 {
			return tid[:16]
		}
		return tid
	}
	return ""
}

// SetCorrelationID stores the correlation ID in context and optionally in baggage.
// If setInBaggage is true, the ID is also added to baggage so it propagates with W3C Baggage.
// The returned context should be used for subsequent calls.
func SetCorrelationID(ctx context.Context, id string, setInBaggage bool) (context.Context, error) {
	ctx = context.WithValue(ctx, correlationIDKey, id)
	if !setInBaggage || id == "" {
		return ctx, nil
	}
	member, err := baggage.NewMember(CORRELATION_ID_BAGGAGE_KEY, id)
	if err != nil {
		return ctx, err
	}
	bag := baggage.FromContext(ctx)
	bag, err = bag.SetMember(member)
	if err != nil {
		return ctx, err
	}
	return baggage.ContextWithBaggage(ctx, bag), nil
}

// RunWithCorrelationID runs fn with the given correlation ID set on context.
// The ID is stored in context; setInBaggage controls whether it is also added to baggage for propagation.
func RunWithCorrelationID[T any](ctx context.Context, id string, setInBaggage bool, fn func(context.Context) (T, error)) (T, error) {
	ctx, err := SetCorrelationID(ctx, id, setInBaggage)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn(ctx)
}

// SetCorrelationIDInBaggage adds the correlation ID to the context's baggage only (no context value).
// Use when you want the ID to propagate to downstream services via W3C Baggage without storing it in context.
// Returns the new context; use it for outgoing requests.
func SetCorrelationIDInBaggage(ctx context.Context, id string) (context.Context, error) {
	if id == "" {
		return ctx, nil
	}
	member, err := baggage.NewMember(CORRELATION_ID_BAGGAGE_KEY, id)
	if err != nil {
		return ctx, err
	}
	bag := baggage.FromContext(ctx)
	bag, err = bag.SetMember(member)
	if err != nil {
		return ctx, err
	}
	return baggage.ContextWithBaggage(ctx, bag), nil
}

// MustSetCorrelationID is like SetCorrelationID but panics on error (e.g. invalid baggage value).
// Use when the ID is known to be short and URL-safe (e.g. from GenerateCorrelationID).
func MustSetCorrelationID(ctx context.Context, id string, setInBaggage bool) context.Context {
	ctx, err := SetCorrelationID(ctx, id, setInBaggage)
	if err != nil {
		panic(err)
	}
	return ctx
}
