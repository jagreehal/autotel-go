package autotel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/internal/rotatefile"
)

// LogRotation says how often a log file rolls over.
type LogRotation string

// The rotations WithLogFile accepts. File names use UTC.
const (
	LogRotationMinutely LogRotation = "minutely"
	LogRotationHourly   LogRotation = "hourly"
	LogRotationDaily    LogRotation = "daily"
	LogRotationNever    LogRotation = "never"
)

// DefaultLogFileCount is how many files LogFile keeps when MaxFiles is zero.
const DefaultLogFileCount = 7

// KeepAllLogFiles as LogFile.MaxFiles turns pruning off.
const KeepAllLogFiles = -1

func (r LogRotation) valid() bool {
	switch r {
	case "", LogRotationMinutely, LogRotationHourly, LogRotationDaily, LogRotationNever:
		return true
	}
	return false
}

func (r LogRotation) layout() string {
	switch r {
	case LogRotationMinutely:
		return "2006-01-02-15-04"
	case LogRotationHourly:
		return "2006-01-02-15"
	case LogRotationNever:
		return ""
	}
	return "2006-01-02"
}

// LogFile configures rolling log files. See WithLogFile.
type LogFile struct {
	// Dir is the directory for the files. It is created if missing.
	Dir string
	// Prefix starts each file name. The default is the service name. Give each
	// service its own prefix or directory: pruning goes by name.
	Prefix string
	// Rotation defaults to LogRotationDaily.
	Rotation LogRotation
	// MaxFiles is how many to keep: 2 or more, 0 for DefaultLogFileCount, or
	// KeepAllLogFiles. LogRotationNever writes one file and prunes nothing.
	MaxFiles int
}

func openLogFile(cfg *Config) (*rotatefile.Writer, error) {
	file := cfg.LogFile
	if file == nil {
		return nil, nil
	}
	if file.Dir == "" {
		return nil, errors.New("autotel: LogFile needs a Dir")
	}
	if !file.Rotation.valid() {
		return nil, fmt.Errorf("autotel: LogFile.Rotation %q is not one of minutely, hourly, daily, never", file.Rotation)
	}
	if file.MaxFiles == 1 {
		return nil, errors.New("autotel: LogFile.MaxFiles must be 2 or more, or KeepAllLogFiles")
	}

	prefix := file.Prefix
	if prefix == "" {
		prefix = strings.NewReplacer("/", "_", "\\", "_").Replace(cfg.ServiceName)
	}
	maxFiles := file.MaxFiles
	if maxFiles == 0 {
		maxFiles = DefaultLogFileCount
	}

	writer, err := rotatefile.Open(rotatefile.Options{
		Dir:      file.Dir,
		Prefix:   prefix,
		Layout:   file.Rotation.layout(),
		MaxFiles: maxFiles,
	})
	if err != nil {
		return nil, fmt.Errorf("autotel: opening log file: %w", err)
	}
	return writer, nil
}

// Where Logger output goes besides the OTLP pipeline. Loggers read these on each
// record, so a package-level logger built before Init picks up what Init decides.
var (
	// logsStayLocal is set once an Init has built no log pipeline.
	logsStayLocal atomic.Bool
	logFileSink   atomic.Pointer[rotatefile.Writer]
)

func installLogSinks(file *rotatefile.Writer, stayLocal bool) {
	logsStayLocal.Store(stayLocal)
	if previous := logFileSink.Swap(file); previous != nil {
		_ = previous.Close()
	}
}

// fileSinkWriter feeds the file handler whatever writer is current.
type fileSinkWriter struct{}

func (fileSinkWriter) Write(p []byte) (int, error) {
	if w := logFileSink.Load(); w != nil {
		return w.Write(p)
	}
	return len(p), nil
}

// sinkHandler sends a record to each place Logger output belongs.
//
// The console handler writes to stderr directly rather than through
// slog.Default, because slog.SetDefault(autotel.Logger("app")) is a reasonable
// thing to do and must not make a handler call itself.
type sinkHandler struct {
	otlp, console, file slog.Handler
}

var localLogOptions = &slog.HandlerOptions{ReplaceAttr: redactLocalLog}

func newSinkHandler(name string) *sinkHandler {
	loggerName := []slog.Attr{slog.String("logger", name)}
	return &sinkHandler{
		otlp:    otelslog.NewHandler(name, otelslog.WithLoggerProvider(otel.GetLoggerProvider())),
		console: slog.NewTextHandler(os.Stderr, localLogOptions).WithAttrs(loggerName),
		file:    slog.NewJSONHandler(fileSinkWriter{}, localLogOptions).WithAttrs(loggerName),
	}
}

func (h *sinkHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if logsStayLocal.Load() {
		return h.console.Enabled(ctx, level)
	}
	return h.otlp.Enabled(ctx, level) || (logFileSink.Load() != nil && h.file.Enabled(ctx, level))
}

func (h *sinkHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	if logsStayLocal.Load() || logFileSink.Load() != nil {
		located := withTraceIDs(ctx, record)
		if logsStayLocal.Load() && h.console.Enabled(ctx, record.Level) {
			errs = append(errs, h.console.Handle(ctx, located))
		}
		if logFileSink.Load() != nil && h.file.Enabled(ctx, record.Level) {
			errs = append(errs, h.file.Handle(ctx, located))
		}
	}
	if !logsStayLocal.Load() {
		errs = append(errs, h.otlp.Handle(ctx, record))
	}
	return errors.Join(errs...)
}

func (h *sinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sinkHandler{h.otlp.WithAttrs(attrs), h.console.WithAttrs(attrs), h.file.WithAttrs(attrs)}
}

func (h *sinkHandler) WithGroup(name string) slog.Handler {
	return &sinkHandler{h.otlp.WithGroup(name), h.console.WithGroup(name), h.file.WithGroup(name)}
}

// withTraceIDs adds what OTLP carries natively to a text record. Under
// WithGroup the IDs land inside the group.
func withTraceIDs(ctx context.Context, record slog.Record) slog.Record {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return record
	}
	record = record.Clone()
	record.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	return record
}

// Logger returns a slog.Logger that exports records through the OTLP log
// pipeline set up by Init. Use the *Context methods (InfoContext, ErrorContext)
// and each record carries the trace and span ID of the span in ctx, so a
// backend can jump from a log line to its trace.
//
// A logger keeps the LoggerProvider that was global when it was built. Before
// the first Init that is OpenTelemetry's forwarding provider, which delegates
// to the pipeline Init then installs, so a package-level logger works. Any
// logger built before a second Init stays on the first pipeline.
//
// When Init built no log pipeline (no endpoint, WithLogs(false) or
// OTEL_LOGS_EXPORTER=none), or after Shutdown, records print to stderr instead
// of vanishing.
// WithLogFile adds rolling files either way.
func Logger(name string) *slog.Logger {
	return slog.New(newSinkHandler(name))
}

// setupLogs builds the OTLP log pipeline. It mirrors setupMetrics: nothing is
// exported unless an endpoint or a custom exporter is configured.
func setupLogs(ctx context.Context, res *resource.Resource, cfg *Config) (*sdklog.LoggerProvider, error) {
	if !cfg.LogsEnabled || cfg.signals.logs.Disabled {
		return nil, nil
	}

	exportersList := cfg.LogExporters
	if len(exportersList) == 0 {
		if cfg.signalEndpoint(cfg.signals.logs, logsPath) == "" {
			return nil, nil
		}
		exp, err := newOTLPLogExporter(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create log exporter: %w", err)
		}
		exportersList = append(exportersList, exp)
	}

	providerOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	if cfg.PIIRedactor != nil {
		providerOpts = append(providerOpts, sdklog.WithProcessor(redactingLogProcessor{cfg.PIIRedactor}))
	}
	for _, exp := range exportersList {
		providerOpts = append(providerOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	}

	return sdklog.NewLoggerProvider(providerOpts...), nil
}

func newOTLPLogExporter(ctx context.Context, cfg *Config) (sdklog.Exporter, error) {
	target := cfg.signalEndpoint(cfg.signals.logs, logsPath)
	headers := cfg.signalHeaders(cfg.signals.logs)
	timeout := cfg.BatchTimeout + 5*time.Second
	if cfg.Protocol == ProtocolHTTP {
		return otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(target),
			otlploghttp.WithHeaders(headers),
			otlploghttp.WithTimeout(timeout),
		)
	}
	return otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(target),
		otlploggrpc.WithHeaders(headers),
		otlploggrpc.WithTimeout(timeout),
	)
}
