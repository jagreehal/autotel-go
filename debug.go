package autotel

import (
	"log"
	"os"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	debugEnabled bool
	debugMu      sync.RWMutex
	debugLogger  *log.Logger
)

func init() {
	// Check for debug environment variable
	if os.Getenv("AUTOTEL_DEBUG") == "true" || os.Getenv("AUTOTEL_DEBUG") == "1" {
		EnableDebug()
	}
}

func IsProduction() bool {
	return strings.ToLower(os.Getenv("ENVIRONMENT")) == "production" || strings.ToLower(os.Getenv("ENV")) == "production"
}

// ShouldEnableDebug decides whether debug should be enabled when not explicitly set.
// If debug is nil, enable in non-production by default.
func ShouldEnableDebug(debug *bool) bool {
	if debug != nil {
		return *debug
	}
	return !IsProduction()
}

// EnableDebug enables debug mode, which logs all span operations.
func EnableDebug() {
	debugMu.Lock()
	defer debugMu.Unlock()
	debugEnabled = true
	if debugLogger == nil {
		debugLogger = log.New(os.Stderr, "[autotel] ", log.LstdFlags)
	}
	debugLogger.Println("Debug mode enabled")
}

// DisableDebug disables debug mode.
func DisableDebug() {
	debugMu.Lock()
	defer debugMu.Unlock()
	debugEnabled = false
}

// IsDebugEnabled returns whether debug mode is enabled.
func IsDebugEnabled() bool {
	debugMu.RLock()
	defer debugMu.RUnlock()
	return debugEnabled
}

// debugPrint logs a debug message if debug mode is enabled.
func debugPrint(format string, args ...any) {
	debugMu.RLock()
	enabled := debugEnabled
	logger := debugLogger
	debugMu.RUnlock()

	if enabled && logger != nil {
		logger.Printf(format, args...)
	}
}

// DebugPrintf is exported for internal helpers (avoid cycles).
func DebugPrintf(format string, args ...any) {
	debugPrint(format, args...)
}

// debugSpanStart logs span creation.
func debugSpanStart(ctx trace.SpanContext, name string) {
	if !IsDebugEnabled() {
		return
	}
	debugPrint("→ Start span: %s [trace_id=%s, span_id=%s]", name, ctx.TraceID().String(), ctx.SpanID().String())
}

// debugSpanAttribute logs attribute setting.
func debugSpanAttribute(ctx trace.SpanContext, key string, value any) {
	if !IsDebugEnabled() {
		return
	}
	debugPrint("  Set attribute: %s=%v [trace_id=%s]", key, value, ctx.TraceID().String())
}

// debugSpanError logs error recording.
func debugSpanError(ctx trace.SpanContext, err error) {
	if !IsDebugEnabled() {
		return
	}
	debugPrint("  Record error: %v [trace_id=%s]", err, ctx.TraceID().String())
}

// DebugPrinter provides a structured way to print debug information.
type DebugPrinter struct {
	logger *log.Logger
}

// NewDebugPrinter creates a new debug printer.
func NewDebugPrinter() *DebugPrinter {
	return &DebugPrinter{
		logger: log.New(os.Stderr, "[autotel] ", log.LstdFlags),
	}
}

// PrintSpan prints span information.
func (d *DebugPrinter) PrintSpan(name string, ctx trace.SpanContext, attrs []attribute.KeyValue) {
	if !IsDebugEnabled() {
		return
	}
	d.logger.Printf("Span: %s", name)
	d.logger.Printf("  TraceID: %s", ctx.TraceID().String())
	d.logger.Printf("  SpanID: %s", ctx.SpanID().String())
	if len(attrs) > 0 {
		d.logger.Printf("  Attributes:")
		for _, attr := range attrs {
			d.logger.Printf("    %s: %v", attr.Key, attr.Value.AsInterface())
		}
	}
}

// PrintTrace prints trace information.
func (d *DebugPrinter) PrintTrace(traceID trace.TraceID, spans []string) {
	if !IsDebugEnabled() {
		return
	}
	d.logger.Printf("Trace: %s", traceID.String())
	d.logger.Printf("  Spans (%d):", len(spans))
	for i, span := range spans {
		d.logger.Printf("    %d. %s", i+1, span)
	}
}
