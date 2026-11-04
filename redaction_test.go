package autotel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/jagreehal/autotel-go/v2/redaction"
)

const piiEmail = "jo@example.com"

// keptLogs keeps a text form of every record it exports.
type keptLogs struct {
	mu    sync.Mutex
	lines []string
}

func (k *keptLogs) Export(_ context.Context, records []sdklog.Record) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, record := range records {
		line := record.Body().String()
		record.WalkAttributes(func(kv attribute.KeyValue) bool {
			line += fmt.Sprintf(" %s=%s", kv.Key, kv.Value.String())
			return true
		})
		k.lines = append(k.lines, line)
	}
	return nil
}

func (*keptLogs) Shutdown(context.Context) error   { return nil }
func (*keptLogs) ForceFlush(context.Context) error { return nil }

func logEverything(logger *slog.Logger, ctx context.Context) {
	logger.With("user", piiEmail).WithGroup("account").InfoContext(ctx, "signed in "+piiEmail,
		"contact", piiEmail,
		"phone", int64(5551234567),
		"cause", errors.New("no user "+piiEmail),
		slog.Group("nested", "email", piiEmail),
		"emails", []string{piiEmail},
		"order.id", "ord_42",
	)
}

func TestRedactionCoversOTLPLogs(t *testing.T) {
	exporter := &keptLogs{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithLogExporters(exporter), WithPIIRedaction())
	require.NoError(t, err)
	logEverything(Logger("x"), context.Background())
	require.NoError(t, Shutdown(context.Background()))

	dump := strings.Join(exporter.lines, "\n")
	require.NotEmpty(t, dump)
	assert.NotContains(t, dump, piiEmail)
	assert.NotContains(t, dump, "5551234567")
	assert.Contains(t, dump, "ord_42")
	assert.Contains(t, dump, "EMAIL_REDACTED")
}

func TestRedactionCoversConsoleAndFileLogs(t *testing.T) {
	stderr := captureStderr(t)
	dir := t.TempDir()
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithLogFile(LogFile{Dir: dir}), WithPIIRedaction())
	require.NoError(t, err)
	ctx, span := Start(context.Background(), "work")
	logEverything(Logger("x"), ctx)
	sc := span.SpanContext()
	span.End()
	require.NoError(t, Shutdown(context.Background()))

	console := stderr()
	line := fmt.Sprint(readLogLines(t, dir)[len(readLogLines(t, dir))-1])
	for name, out := range map[string]string{"console": console, "file": line} {
		assert.NotContains(t, out, piiEmail, name)
		assert.NotContains(t, out, "5551234567", name)
		assert.Contains(t, out, "ord_42", name)
		assert.Contains(t, out, sc.TraceID().String(), name+" keeps the trace id")
	}
}

func TestAllowlistAppliesToLogs(t *testing.T) {
	exporter := &keptLogs{}
	stderr := captureStderr(t)
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithLogExporters(exporter), WithPIIRedaction(redaction.WithAllowlistKeys("support.email")))
	require.NoError(t, err)
	Logger("x").Info("ticket", "support.email", "help@example.com")
	require.NoError(t, Shutdown(context.Background()))
	_ = stderr()
	assert.Contains(t, strings.Join(exporter.lines, ""), "help@example.com")
}

func TestLogsPassThroughWithoutARedactor(t *testing.T) {
	exporter := &keptLogs{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithLogExporters(exporter))
	require.NoError(t, err)
	Logger("x").Info("hi", "email", piiEmail)
	require.NoError(t, Shutdown(context.Background()))
	assert.Contains(t, strings.Join(exporter.lines, ""), piiEmail)
}

type metricValues struct {
	mu   sync.Mutex
	data []metricdata.Metrics
}

func (*metricValues) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (*metricValues) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (m *metricValues) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, scope := range rm.ScopeMetrics {
		m.data = append(m.data, scope.Metrics...)
	}
	return nil
}

func (*metricValues) ForceFlush(context.Context) error { return nil }
func (*metricValues) Shutdown(context.Context) error   { return nil }

func TestRedactionCoversMetricsAndMergesTheirSeries(t *testing.T) {
	exporter := &metricValues{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithMetricExporters(exporter), WithPIIRedaction())
	require.NoError(t, err)
	ctx := context.Background()
	Meter().Counter(ctx, "signups", 1, map[string]any{"user": piiEmail, "plan": "pro"})
	Meter().Counter(ctx, "signups", 1, map[string]any{"user": "bo@example.com", "plan": "pro"})
	Meter().Histogram(ctx, "latency", 1, map[string]any{"user": piiEmail})
	require.NoError(t, Shutdown(context.Background()))

	require.NotEmpty(t, exporter.data)
	assert.NotContains(t, fmt.Sprintf("%+v", exporter.data), "example.com")

	for _, m := range exporter.data {
		if m.Name == "signups" {
			points := m.Data.(metricdata.Sum[float64]).DataPoints
			require.Len(t, points, 1, "two users must be one series, or backends see duplicates")
			assert.EqualValues(t, 2, points[0].Value)
			plan, _ := points[0].Attributes.Value("plan")
			assert.Equal(t, "pro", plan.AsString())
		}
	}
}

func TestDebugOutputNeverPrintsTheOriginal(t *testing.T) {
	var out strings.Builder
	_, err := Init(context.Background(), WithDebug(true), WithService("x"), WithPIIRedaction())
	require.NoError(t, err)
	t.Cleanup(DisableDebug)

	debugMu.Lock()
	previous := debugLogger
	debugLogger = log.New(&out, "", 0)
	debugMu.Unlock()
	t.Cleanup(func() {
		debugMu.Lock()
		defer debugMu.Unlock()
		debugLogger = previous
	})

	_, span := Start(context.Background(), "work")
	span.SetAttribute("user", piiEmail)
	span.RecordError(errors.New("no user " + piiEmail))
	span.End()

	assert.Contains(t, out.String(), "Set attribute", "debug output must be what this test reads")
	assert.Contains(t, out.String(), "Record error")
	assert.NotContains(t, out.String(), piiEmail)
	require.NoError(t, Shutdown(context.Background()))
}

// A 16-digit span ID looks like a card number.
func TestLocalLogsKeepAllDigitIDs(t *testing.T) {
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithPIIRedaction())
	require.NoError(t, err)
	defer func() { _ = Shutdown(context.Background()) }()

	id := "4111111111111111"
	assert.Equal(t, id, redactLocalLog(nil, slog.String("span_id", id)).Value.String())
	assert.NotEqual(t, id, redactLocalLog(nil, slog.String("card", id)).Value.String())
}

func TestSecondInitWithoutRedactionDropsTheRedactor(t *testing.T) {
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithPIIRedaction())
	require.NoError(t, err)
	require.NoError(t, Shutdown(context.Background()))
	exporter := &keepingExporter{}
	_, err = Init(context.Background(), WithDebug(false), WithService("x"), WithSpanExporters(exporter))
	require.NoError(t, err)
	_, span := otel.Tracer("t").Start(context.Background(), "work")
	span.End()
	_, wrapped := Start(context.Background(), "w")
	wrapped.SetAttribute("user", piiEmail)
	wrapped.End()
	require.NoError(t, Shutdown(context.Background()))
	found := false
	for _, s := range exporter.spans {
		for _, kv := range s.Attributes() {
			found = found || kv.Value.AsString() == piiEmail
		}
	}
	assert.True(t, found)
}

type recordingTracker struct {
	mu     sync.Mutex
	events []map[string]any
}

func (r *recordingTracker) Track(_ context.Context, _ string, properties map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, properties)
}

func (*recordingTracker) Shutdown(context.Context) error { return nil }

func TestRedactionCoversEveryTrackHelper(t *testing.T) {
	tracker := &recordingTracker{}
	previous := queueFactory
	t.Cleanup(func() { queueFactory = previous })
	queueFactory = func(*Config, []Subscriber) EventTracker { return tracker }

	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithSubscribers(struct{ Subscriber }{}), WithPIIRedaction())
	require.NoError(t, err)

	ctx := context.Background()
	props := map[string]any{"email": piiEmail, "nested": map[string]any{"email": piiEmail}, "amount": 12.5}
	Track(ctx, "order", props)
	TrackValue(ctx, "revenue", 10, props)
	TrackOutcome(ctx, "pay", "success", props)
	require.NoError(t, Shutdown(ctx))

	require.Len(t, tracker.events, 3)
	assert.NotContains(t, fmt.Sprint(tracker.events), piiEmail)
	assert.Equal(t, 12.5, tracker.events[0]["amount"], "values with nothing to hide keep their type")
	assert.Equal(t, piiEmail, props["email"], "the caller's map stays as it was")
}

func TestRedactionCoversMetricSliceValues(t *testing.T) {
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithPIIRedaction())
	require.NoError(t, err)
	defer func() { _ = Shutdown(context.Background()) }()
	got := toAttributes(map[string]any{"emails": []string{piiEmail}})
	assert.NotContains(t, got[0].Value.String(), piiEmail)
}
