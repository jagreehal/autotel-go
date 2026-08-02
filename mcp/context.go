package mcp

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/propagation"
)

// Meta keys for W3C Trace Context propagation in MCP params._meta.
const (
	MetaKeyTraceparent = "traceparent"
	MetaKeyTracestate  = "tracestate"
	MetaKeyBaggage     = "baggage"
)

var defaultPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// ExtractContextFromMeta extracts OpenTelemetry context from an MCP request's params._meta.
//
// This enables distributed tracing across MCP client-server boundaries by reading
// traceparent, tracestate, and baggage from the _meta map (e.g. from JSON-RPC params._meta).
// If meta is nil or empty, the given ctx is returned unchanged.
//
// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/#context-propagation
func ExtractContextFromMeta(ctx context.Context, meta map[string]any) context.Context {
	if meta == nil {
		return ctx
	}
	carrier := make(map[string]string)
	for _, key := range []string{MetaKeyTraceparent, MetaKeyTracestate, MetaKeyBaggage} {
		if v, ok := meta[key]; ok {
			if s, ok := toString(v); ok && s != "" {
				carrier[key] = s
			}
		}
	}
	if len(carrier) == 0 {
		return ctx
	}
	return defaultPropagator.Extract(ctx, propagation.MapCarrier(carrier))
}

// InjectContextToMeta injects the current OpenTelemetry context into a map suitable
// for MCP params._meta.
//
// The returned map can be merged into params._meta when sending MCP requests so that
// the server can continue the trace. Keys set are traceparent, tracestate, and baggage
// (when present).
func InjectContextToMeta(ctx context.Context) map[string]any {
	carrier := make(map[string]string)
	defaultPropagator.Inject(ctx, propagation.MapCarrier(carrier))
	out := make(map[string]any, len(carrier))
	for k, v := range carrier {
		out[k] = v
	}
	return out
}

// MergeMeta merges injected trace context into an existing _meta map (e.g. from
// incoming params). Use when the client sends a request and you want to add or
// override trace context in params._meta. Does not modify base; returns a new map.
func MergeMeta(base map[string]any, injected map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range base {
		out[k] = v
	}
	for k, v := range injected {
		out[k] = v
	}
	return out
}

func toString(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case fmt.Stringer:
		return t.String(), true
	default:
		return "", false
	}
}
