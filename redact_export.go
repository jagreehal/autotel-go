package autotel

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2/redaction"
)

// textRedactor is the part of redaction.PIIRedactor this package uses.
type textRedactor interface {
	Redact(key, value string) string
}

// redactingExporter applies WithPIIRedaction to every span on its way out,
// whoever created it. Middleware from otelgin, otelchi, otelhttp and the like
// never goes through autotel.Start, so redacting there alone would miss the URL
// paths that carry the most personal data.
type redactingExporter struct {
	trace.SpanExporter
	redactor *redaction.PIIRedactor
}

func (e *redactingExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	redacted := make([]trace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		redacted[i] = e.redact(span)
	}
	return e.SpanExporter.ExportSpans(ctx, redacted)
}

func (e *redactingExporter) redact(span trace.ReadOnlySpan) trace.ReadOnlySpan {
	events := span.Events()
	copied := make([]trace.Event, len(events))
	for i, event := range events {
		event.Attributes = e.attributes(event.Attributes)
		copied[i] = event
	}

	status := span.Status()
	status.Description = e.redactor.Redact("status.description", status.Description)

	return redactedSpan{
		ReadOnlySpan: span,
		name:         e.redactor.Redact("span.name", span.Name()),
		attributes:   e.attributes(span.Attributes()),
		events:       copied,
		status:       status,
	}
}

func (e *redactingExporter) attributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	return redactAttributes(e.redactor, attrs)
}

func redactAttributes(r textRedactor, attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(attrs))
	for i, kv := range attrs {
		out[i] = attribute.KeyValue{Key: kv.Key, Value: redactValue(r, string(kv.Key), kv.Value)}
	}
	return out
}

// redactValue also checks numbers: 5551234567 is a phone number either way.
func redactValue(r textRedactor, key string, value attribute.Value) attribute.Value {
	switch value.Type() {
	case attribute.STRING:
		return attribute.StringValue(r.Redact(key, value.AsString()))
	case attribute.INT64:
		text := value.String()
		if redacted := r.Redact(key, text); redacted != text {
			return attribute.StringValue(redacted)
		}
	case attribute.STRINGSLICE:
		values := slices.Clone(value.AsStringSlice())
		for i, item := range values {
			values[i] = r.Redact(key, item)
		}
		return attribute.StringSliceValue(values)
	case attribute.SLICE:
		values := slices.Clone(value.AsSlice())
		for i, item := range values {
			values[i] = redactValue(r, key, item)
		}
		return attribute.SliceValue(values...)
	case attribute.MAP:
		return attribute.MapValue(redactAttributes(r, value.AsMap())...)
	}
	return value
}

// redactingLogProcessor scrubs each record at emit, ahead of the batch processor
// that exports it. Working on the record covers every logger, however and
// whenever it was built.
type redactingLogProcessor struct {
	redactor textRedactor
}

func (p redactingLogProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	record.SetBody(redactValue(p.redactor, "log.body", record.Body()))

	attrs := make([]attribute.KeyValue, 0, record.AttributesLen())
	record.WalkAttributes(func(kv attribute.KeyValue) bool {
		attrs = append(attrs, kv)
		return true
	})
	record.SetAttributes(redactAttributes(p.redactor, attrs)...)
	return nil
}

func (redactingLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (redactingLogProcessor) Shutdown(context.Context) error                         { return nil }
func (redactingLogProcessor) ForceFlush(context.Context) error                       { return nil }

// redactingTracker scrubs event properties before subscribers send them to
// analytics vendors. Every Track helper ends here.
type redactingTracker struct {
	EventTracker
	redactor textRedactor
}

func (t *redactingTracker) Track(ctx context.Context, event string, properties map[string]any) {
	t.EventTracker.Track(ctx, event, redactProperties(t.redactor, properties))
}

func redactProperties(r textRedactor, properties map[string]any) map[string]any {
	out := make(map[string]any, len(properties))
	for key, value := range properties {
		if nested, ok := value.(map[string]any); ok {
			out[key] = redactProperties(r, nested)
			continue
		}
		text := fmt.Sprint(value)
		if redacted := r.Redact(key, text); redacted != text {
			out[key] = redacted
			continue
		}
		out[key] = value
	}
	return out
}

// redactLocalLog is the slog ReplaceAttr for what Logger writes to stderr and
// files. It sees the message and each attribute, in groups too.
//
// ponytail: slog formats logger.With values when With runs, so a With called
// before Init reaches these two sinks unredacted. OTLP is unaffected.
func redactLocalLog(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey, slog.LevelKey, "logger", "trace_id", "span_id":
		return attr
	}
	redactor := currentRedactor()
	if redactor == nil {
		return attr
	}
	text := attr.Value.String()
	if redacted := redactor.Redact(attr.Key, text); redacted != text {
		attr.Value = slog.StringValue(redacted)
	}
	return attr
}

// redactedSpan embeds the original for everything that can't hold personal data.
type redactedSpan struct {
	trace.ReadOnlySpan
	name       string
	attributes []attribute.KeyValue
	events     []trace.Event
	status     trace.Status
}

func (s redactedSpan) Name() string                     { return s.name }
func (s redactedSpan) Attributes() []attribute.KeyValue { return s.attributes }
func (s redactedSpan) Events() []trace.Event            { return s.events }
func (s redactedSpan) Status() trace.Status             { return s.status }
