package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// WithTraceContext returns slog attributes for the current trace context.
// This can be used to enrich log records with trace and span IDs.
//
// Example:
//
//	logger.InfoContext(ctx, "processing user",
//	    slog.String("user_id", userID),
//	    logging.WithTraceContext(ctx)...,
//	)
func WithTraceContext(ctx context.Context) []slog.Attr {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return nil
	}

	sc := span.SpanContext()
	if !sc.IsValid() {
		return nil
	}

	return []slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}
}

// TraceHandler wraps a slog.Handler to automatically inject trace context.
// This handler automatically adds trace_id and span_id to all log records
// when a valid trace context is present.
//
// Example:
//
//	logger := slog.New(logging.NewTraceHandler(
//	    slog.NewJSONHandler(os.Stdout, nil),
//	))
type TraceHandler struct {
	handler slog.Handler
	attrs   []slog.Attr
}

// NewTraceHandler creates a handler that automatically adds trace context.
func NewTraceHandler(h slog.Handler) *TraceHandler {
	return &TraceHandler{handler: h}
}

// Enabled reports whether the handler handles records at the given level.
func (h *TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle adds trace context before delegating to wrapped handler.
func (h *TraceHandler) Handle(ctx context.Context, r slog.Record) error {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		sc := span.SpanContext()
		if sc.IsValid() {
			r.AddAttrs(
				slog.String("trace_id", sc.TraceID().String()),
				slog.String("span_id", sc.SpanID().String()),
				slog.String("trace_flags", sc.TraceFlags().String()),
			)
		}
	}
	if len(h.attrs) > 0 {
		r.AddAttrs(h.attrs...)
	}
	return h.handler.Handle(ctx, r)
}

// WithAttrs returns a new handler with the given attributes.
func (h *TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceHandler{handler: h.handler.WithAttrs(attrs), attrs: h.attrs}
}

// WithGroup returns a new handler with the given group.
func (h *TraceHandler) WithGroup(name string) slog.Handler {
	return &TraceHandler{handler: h.handler.WithGroup(name)}
}
