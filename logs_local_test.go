package autotel

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func captureStderr(t *testing.T) func() string {
	t.Helper()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	previous := os.Stderr
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = previous })
	return func() string {
		_ = write.Close()
		body, err := io.ReadAll(read)
		require.NoError(t, err)
		return string(body)
	}
}

func TestLoggerPrintsLocallyWhenNothingExports(t *testing.T) {
	stderr := captureStderr(t)
	logger := Logger("orders") // built before Init, as a package-level logger is

	_, err := Init(context.Background(), WithDebug(false), WithService("orders"))
	require.NoError(t, err)
	ctx, span := Start(context.Background(), "work")
	logger.InfoContext(ctx, "order created", "order.id", "ord_42")
	sc := span.SpanContext()
	span.End()
	require.NoError(t, Shutdown(context.Background()))

	out := stderr()
	assert.Contains(t, out, "order created")
	assert.Contains(t, out, "order.id=ord_42")
	assert.Contains(t, out, "logger=orders")
	assert.Contains(t, out, "trace_id="+sc.TraceID().String())
}

func TestLoggerPrintsAfterShutdownRatherThanDropping(t *testing.T) {
	stderr := captureStderr(t)
	_, url := newReceiver(t)
	logger := Logger("x")
	emit(t, WithEndpoint(url))
	logger.Info("after shutdown")
	assert.Contains(t, stderr(), "after shutdown")
}

func TestSecondInitClosesThePreviousLogFile(t *testing.T) {
	_ = captureStderr(t)
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithLogFile(LogFile{Dir: t.TempDir()}))
	require.NoError(t, err)
	first := logFileSink.Load()
	_, err = Init(context.Background(), WithDebug(false), WithService("x"), WithLogFile(LogFile{Dir: t.TempDir()}))
	require.NoError(t, err)
	_, writeErr := first.Write([]byte("x"))
	assert.ErrorIs(t, writeErr, os.ErrClosed)
	require.NoError(t, Shutdown(context.Background()))
}

// Past five attributes a record's storage is shared between copies, so two
// handlers adding to it would overwrite each other.
func TestTraceIDsDoNotAlterASharedRecord(t *testing.T) {
	spanContext := func(id byte) context.Context {
		return oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID: oteltrace.TraceID{id}, SpanID: oteltrace.SpanID{id},
		}))
	}
	traceID := func(record slog.Record) string {
		var found string
		record.Attrs(func(a slog.Attr) bool {
			if a.Key == "trace_id" {
				found = a.Value.String()
			}
			return true
		})
		return found
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "m", 0)
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		record.AddAttrs(slog.String(key, "v"))
	}
	first := withTraceIDs(spanContext(1), record)
	second := withTraceIDs(spanContext(2), record)
	assert.NotEqual(t, traceID(first), traceID(second))

	// slog marks a record whose storage a copy has already written to.
	second.Attrs(func(a slog.Attr) bool {
		assert.NotEqual(t, "!BUG", a.Key, a.Value.String())
		return true
	})
}

func TestLoggerDoesNotPrintWhenLogsExport(t *testing.T) {
	stderr := captureStderr(t)
	r, url := newReceiver(t)
	emit(t, WithEndpoint(url))
	assert.NotContains(t, stderr(), "hello")
	assert.Contains(t, r.paths(), "/v1/logs")
}

// A logger installed as the slog default must not end up calling itself.
func TestLoggerAsSlogDefault(t *testing.T) {
	stderr := captureStderr(t)
	previous, previousLog, previousFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		// SetDefault also repoints the log package, and undoing it doesn't.
		slog.SetDefault(previous)
		log.SetOutput(previousLog)
		log.SetFlags(previousFlags)
	})

	slog.SetDefault(Logger("app"))
	_, err := Init(context.Background(), WithDebug(false), WithService("app"))
	require.NoError(t, err)
	slog.Info("via default")
	require.NoError(t, Shutdown(context.Background()))
	assert.Contains(t, stderr(), "via default")
}

func readLogLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	body, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	var lines []map[string]any
	for line := range strings.Lines(string(body)) {
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &parsed))
		lines = append(lines, parsed)
	}
	return lines
}

func TestWithLogFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	_ = captureStderr(t)
	logger := Logger("orders").With("region", "iad")

	_, err := Init(context.Background(), WithDebug(false), WithService("a/b"), WithLogFile(LogFile{Dir: dir}))
	require.NoError(t, err)
	ctx, span := Start(context.Background(), "work")
	logger.InfoContext(ctx, "to file")
	sc := span.SpanContext()
	span.End()
	require.NoError(t, Shutdown(context.Background()))

	// After Shutdown a logger must neither crash nor write to a closed file.
	logger.Info("late")

	lines := readLogLines(t, dir)
	require.Len(t, lines, 1)
	assert.Equal(t, "to file", lines[0]["msg"])
	assert.Equal(t, "iad", lines[0]["region"])
	assert.Equal(t, sc.TraceID().String(), lines[0]["trace_id"])
}

func TestLogFileFromEnvAndExportTogether(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUTOTEL_LOG_FILE_DIR", dir)
	t.Setenv("AUTOTEL_LOG_FILE_ROTATION", "never")
	r, url := newReceiver(t)
	emit(t, WithEndpoint(url))

	assert.Contains(t, r.paths(), "/v1/logs")
	assert.Equal(t, "hello", readLogLines(t, dir)[0]["msg"])
}

func TestInvalidLogFileFailsInit(t *testing.T) {
	for name, file := range map[string]LogFile{
		"no dir":   {},
		"rotation": {Dir: t.TempDir(), Rotation: "weekly"},
		"one file": {Dir: t.TempDir(), MaxFiles: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Init(context.Background(), WithDebug(false), WithLogFile(file))
			assert.Error(t, err)
		})
	}
}

// A second Init replaces the file; shutting down the first must not close it.
func TestStaleShutdownLeavesNewLogFileOpen(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	_ = captureStderr(t)
	cleanup, err := Init(context.Background(), WithDebug(false), WithService("x"), WithLogFile(LogFile{Dir: first}))
	require.NoError(t, err)
	_, err = Init(context.Background(), WithDebug(false), WithService("x"), WithLogFile(LogFile{Dir: second}))
	require.NoError(t, err)

	cleanup()
	Logger("x").Info("still here")
	require.NoError(t, Shutdown(context.Background()))
	assert.Equal(t, "still here", readLogLines(t, second)[0]["msg"])
}

type metricNames struct {
	mu    sync.Mutex
	names map[string]bool
}

func (*metricNames) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (*metricNames) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (m *metricNames) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			m.names[metric.Name] = true
		}
	}
	return nil
}

func (*metricNames) ForceFlush(context.Context) error { return nil }
func (*metricNames) Shutdown(context.Context) error   { return nil }

func TestWithRuntimeMetrics(t *testing.T) {
	exporter := &metricNames{names: map[string]bool{}}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithMetricExporters(exporter), WithRuntimeMetrics())
	require.NoError(t, err)
	require.NoError(t, Shutdown(context.Background()))
	assert.True(t, exporter.names["go.goroutine.count"], "got %v", exporter.names)
}
