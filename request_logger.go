package autotel

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// RequestLogger gathers everything one unit of work learned onto one record:
// the span in the context it was made from. It is the wide event, also called
// a canonical log line.
//
//	event := autotel.NewRequestLogger(ctx)
//	defer event.EmitNow()
//	event.Set(map[string]any{"atm": map[string]any{"branch": branch}})
//
// Nested maps flatten to dotted attributes, so the call above and
// Set(map[string]any{"atm.branch": branch}) record the same field. Every Set
// lands on the span straight away, so a request that fails halfway still
// carries what it knew before failing.
//
// EmitNow seals the record and adds one "log.emit.manual" event to the span
// with every field. Set after EmitNow is ignored with a warning: data added
// after the record was sent would appear in no backend.
type RequestLogger struct {
	span trace.Span

	mu      sync.Mutex
	fields  map[string]any
	emitted *RequestLogSnapshot
}

// RequestLogSnapshot is what EmitNow sent.
type RequestLogSnapshot struct {
	Timestamp time.Time
	TraceID   string
	SpanID    string
	// Fields is every field set, flattened to dotted keys.
	Fields map[string]any
}

// NewRequestLogger returns a request logger writing to the span in ctx. With
// no recording span in ctx (autotel not initialised, or the request not
// sampled) it records nothing and every method is safe to call.
func NewRequestLogger(ctx context.Context) *RequestLogger {
	return &RequestLogger{span: trace.SpanFromContext(ctx), fields: map[string]any{}}
}

// Set merges fields into the record and onto the span. A later value for the
// same key replaces an earlier one.
func (l *RequestLogger) Set(fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.sealed("Set") {
		return
	}

	flat := flatten(fields)
	maps.Copy(l.fields, flat)
	l.setOnSpan(flat)
}

// Info adds a "log.info" event to the span, and merges fields into the record.
func (l *RequestLogger) Info(message string, fields map[string]any) {
	l.event("log.info", message, fields)
}

// Warn adds a "log.warn" event to the span, and merges fields into the record.
func (l *RequestLogger) Warn(message string, fields map[string]any) {
	l.event("log.warn", message, fields)
}

// Error records err on the span (status ERROR, the exception event, and the
// error.* fields of a StructuredError) and merges fields into the record.
func (l *RequestLogger) Error(err error, fields map[string]any) {
	if err == nil {
		err = errors.New("unknown error")
	}

	if !l.event("log.error", err.Error(), fields) {
		return
	}
	(&spanImpl{span: l.span}).RecordError(err)
}

// Fields returns a copy of everything set so far, flattened to dotted keys.
func (l *RequestLogger) Fields() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()

	return maps.Clone(l.fields)
}

// EmitNow seals the record and sends it as one event on the span. It is
// meant to run once per unit of work, typically deferred, so that success
// and failure both produce exactly one record. A second call returns the
// first snapshot and sends nothing.
func (l *RequestLogger) EmitNow() RequestLogSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.emitted != nil {
		return *l.emitted
	}

	sc := l.span.SpanContext()
	snapshot := RequestLogSnapshot{
		Timestamp: time.Now(),
		Fields:    maps.Clone(l.fields),
	}
	if sc.IsValid() {
		snapshot.TraceID = sc.TraceID().String()
		snapshot.SpanID = sc.SpanID().String()
	}

	if l.span.IsRecording() {
		l.span.AddEvent("log.emit.manual", trace.WithAttributes(attributesFrom(snapshot.Fields)...))
	}

	l.emitted = &snapshot

	return snapshot
}

// event reports false, recording nothing, once the record has been sent.
func (l *RequestLogger) event(name, message string, fields map[string]any) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.sealed(name) {
		return false
	}

	flat := flatten(fields)
	maps.Copy(l.fields, flat)
	l.setOnSpan(flat)

	if l.span.IsRecording() {
		attrs := attributesFrom(flat)
		attrs = append(attrs, redactedAttribute("message", message))
		l.span.AddEvent(name, trace.WithAttributes(attrs...))
	}

	return true
}

// sealed reports, and warns, when the record has already been sent.
func (l *RequestLogger) sealed(method string) bool {
	if l.emitted == nil {
		return false
	}

	slog.Warn("[autotel] RequestLogger."+method+" called after EmitNow; the data will not appear in the emitted record",
		"trace_id", l.emitted.TraceID)

	return true
}

func (l *RequestLogger) setOnSpan(flat map[string]any) {
	if !l.span.IsRecording() {
		return
	}

	s := &spanImpl{span: l.span}
	for key, value := range flat {
		s.SetAttribute(key, value)
	}
}
