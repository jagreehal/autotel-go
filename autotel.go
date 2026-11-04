package autotel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlpmetrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/circuitbreaker"
	"github.com/jagreehal/autotel-go/v2/internal/exporters"
	"github.com/jagreehal/autotel-go/v2/internal/rotatefile"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/sampling"
)

// EventTracker is an interface for tracking analytics events.
// This avoids import cycles by not importing the analytics package directly.
type EventTracker interface {
	Track(ctx context.Context, event string, properties map[string]any)
	Shutdown(ctx context.Context) error
}

var (
	globalTracker   EventTracker
	globalTrackerMu sync.RWMutex
	queueFactory    func(cfg *Config, subscribers []Subscriber) EventTracker
)

// errNoQueue explains the one mistake a custom subscriber makes easy. The queue
// lives in the subscribers package, which only the built-in subscribers import.
var errNoQueue = errors.New("autotel: WithSubscribers needs the event queue: " +
	`add import _ "github.com/jagreehal/autotel-go/v2/subscribers"`)

// RegisterQueueFactory registers a function to create analytics queues.
// This is called by the analytics package to avoid import cycles.
// Users should not call this directly.
func RegisterQueueFactory(factory func(cfg *Config, subscribers []Subscriber) EventTracker) {
	queueFactory = factory
}

// Init initializes autotel with OpenTelemetry SDK using functional options.
// Returns a cleanup function that should be called on shutdown.
//
// This is the recommended way to initialize autotel for most users.
//
// Example:
//
//	cleanup, err := autotel.Init(ctx,
//	    autotel.WithService("my-service"),
//	    autotel.WithEndpoint("http://localhost:4318"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer cleanup()
func Init(ctx context.Context, opts ...Option) (func(), error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.Subscribers) > 0 && queueFactory == nil {
		cfg.OptionError(errNoQueue)
	}
	// Report option validation failures before building anything, so a missing
	// API key fails loudly at startup instead of exporting into the void.
	if err := errors.Join(cfg.optionErrors...); err != nil {
		return nil, err
	}
	return initWithConfig(ctx, cfg)
}

// InitWithConfig initializes autotel with a custom Config.
// This provides advanced users with full control over configuration.
//
// Most users should use Init() with functional options instead.
//
// Example:
//
//	cfg := autotel.DefaultConfig()
//	cfg.ServiceName = "my-service"
//	cfg.Endpoint = "custom:4318"
//	cfg.Sampler = trace.AlwaysSample() // Custom sampler
//	cleanup, err := autotel.InitWithConfig(ctx, cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer cleanup()
func InitWithConfig(ctx context.Context, cfg *Config) (func(), error) {
	return initWithConfig(ctx, cfg)
}

func initWithConfig(ctx context.Context, cfg *Config) (func(), error) {
	mergedCfg := resolveAndMergeConfig(cfg)
	if err := errors.Join(mergedCfg.optionErrors...); err != nil {
		return nil, err
	}
	fillZeroValues(mergedCfg)
	setupDebugMode(mergedCfg)

	res, err := buildResource(ctx, mergedCfg)
	if err != nil {
		return nil, err
	}

	mp, err := setupMetrics(ctx, res, mergedCfg)
	if err != nil {
		return nil, err
	}

	lp, err := setupLogs(ctx, res, mergedCfg)
	if err != nil {
		shutdownPartial(mp, nil)
		return nil, err
	}

	tp, err := buildTracerProvider(ctx, res, mergedCfg)
	if err != nil {
		shutdownPartial(mp, lp)
		return nil, err
	}

	// Last of the steps that can fail: nothing below needs unwinding.
	file, err := openLogFile(mergedCfg)
	if err != nil {
		_ = tp.Shutdown(ctx)
		shutdownPartial(mp, lp)
		return nil, err
	}

	// Nothing global changes until every step has worked, so a failed Init leaves
	// a running pipeline as it was.
	installLogSinks(file, lp == nil)
	if mp != nil {
		otel.SetMeterProvider(mp)
	}
	if lp != nil {
		otel.SetLoggerProvider(lp)
	}

	otel.SetTracerProvider(tp)
	// The global propagator lets otelhttp and otelgrpc middleware continue the
	// trace named in an incoming traceparent header.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tracker := setupGlobalFeatures(mergedCfg)
	announce(mergedCfg, mp != nil, lp != nil)

	return createCleanupFunc(tp, mp, lp, file, tracker, mergedCfg.spanExportFailure), nil
}

// announce logs where each signal is going, so a missing endpoint shows at
// startup rather than as an empty backend. It goes through slog.Default, so an
// application's own logger and level apply.
func announce(cfg *Config, hasMetrics, hasLogs bool) {
	var targets []string
	if hasOTLPTraces(cfg) {
		targets = append(targets, "traces to "+displayEndpoint(cfg.signalEndpoint(cfg.signals.traces, tracesPath), cfg.Protocol))
	}
	if hasMetrics && len(cfg.MetricExporters) == 0 {
		targets = append(targets, "metrics to "+displayEndpoint(cfg.signalEndpoint(cfg.signals.metrics, metricsPath), cfg.Protocol))
	}
	if hasLogs && len(cfg.LogExporters) == 0 {
		targets = append(targets, "logs to "+displayEndpoint(cfg.signalEndpoint(cfg.signals.logs, logsPath), cfg.Protocol))
	}

	switch {
	case len(targets) > 0:
		protocol := "OTLP/HTTP"
		if cfg.Protocol == ProtocolGRPC {
			protocol = "OTLP/gRPC"
		}
		slog.Info(fmt.Sprintf("autotel: %s: exporting %s over %s", cfg.ServiceName, strings.Join(targets, ", "), protocol))
	case len(cfg.SpanExporters)+len(cfg.MetricExporters)+len(cfg.LogExporters) == 0:
		slog.Info(fmt.Sprintf("autotel: %s: no OTLP endpoint, telemetry stays local. Set OTEL_EXPORTER_OTLP_ENDPOINT to export", cfg.ServiceName))
	}
}

// displayEndpoint makes an endpoint safe for a log line.
func displayEndpoint(endpoint string, protocol Protocol) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "(unparseable endpoint)"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if protocol == ProtocolGRPC {
		parsed.Path = "" // gRPC routes by service, so the signal path isn't used
	}
	return parsed.String()
}

// resolveAndMergeConfig resolves configs from all sources and merges them.
func resolveAndMergeConfig(explicit *Config) *Config {
	envCfg := resolveEnvConfig()

	yamlCfg, yamlErr := loadYamlConfigFromAutotel()

	merged := mergeConfigs(explicit, yamlCfg, envCfg)
	merged.OptionError(yamlErr)
	if yamlCfg != nil {
		merged.optionErrors = append(merged.optionErrors, yamlCfg.optionErrors...)
	}
	merged.optionErrors = append(merged.optionErrors, envCfg.optionErrors...)
	// A bad variable only matters if it is the one in charge.
	if !merged.protocolChosen {
		merged.OptionError(envCfg.protocolEnvErr)
	}
	if !merged.samplerChosen {
		merged.OptionError(envCfg.samplerEnvErr)
	}
	applyBackendPreset(merged)
	applyDevtools(merged)
	merged.Endpoint = sanitizeEndpoint(merged.Endpoint)
	applyBuildInfo(merged)
	return merged
}

// setupDebugMode configures debug mode based on config.
func setupDebugMode(cfg *Config) {
	cfg.debugRequested = cfg.Debug != nil && *cfg.Debug || envDebugSet()
	if cfg.Debug == nil {
		enabled := ShouldEnableDebug(nil)
		cfg.Debug = &enabled
	}

	if *cfg.Debug {
		EnableDebug()
	} else {
		DisableDebug()
	}
}

// buildResource creates the OpenTelemetry resource with all attributes.
func buildResource(ctx context.Context, cfg *Config) (*resource.Resource, error) {
	attrs := make([]attribute.KeyValue, 0, len(cfg.ResourceAttributes)+3)
	for k, v := range cfg.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}

	// Later entries win. A value somebody set beats the same key in resource
	// attributes; a default gives way to it.
	identity := func(inAttrs, set bool, attr attribute.KeyValue) {
		if !inAttrs || set {
			attrs = append(attrs, attr)
		}
	}
	inAttrs := func(keys ...string) bool {
		return slices.ContainsFunc(keys, func(key string) bool {
			_, found := cfg.ResourceAttributes[key]
			return found
		})
	}
	identity(inAttrs("service.name"), cfg.ServiceName != defaultServiceName, semconv.ServiceName(cfg.ServiceName))
	identity(inAttrs("service.version"), cfg.versionChosen || cfg.ServiceVersion != defaultServiceVersion,
		semconv.ServiceVersion(cfg.ServiceVersion))
	identity(inAttrs("deployment.environment", "deployment.environment.name"),
		cfg.environmentChosen || cfg.Environment != defaultEnvironment,
		semconv.DeploymentEnvironment(cfg.Environment))

	// No resource.WithFromEnv: resolveEnvConfig already read those variables, and
	// the detector would put them above explicit options.
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
		// Not WithProcess: it adds the command line, and flags can hold secrets.
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithOS(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}
	return res, nil
}

// buildTracerProvider creates the tracer provider with exporters and processors.
func buildTracerProvider(ctx context.Context, res *resource.Resource, cfg *Config) (*trace.TracerProvider, error) {
	exportersList, err := buildExporters(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tailPolicy := endPolicyFor(cfg)
	processors := buildSpanProcessors(exportersList, cfg, tailPolicy)
	sampler := selectSampler(cfg, tailPolicy != nil)
	if cfg.DebugCapture {
		sampler = debugCaptureSampler{next: sampler}
	}
	sampler = withGuards(sampler, cfg)

	providerOpts := []trace.TracerProviderOption{
		trace.WithResource(res),
		trace.WithSampler(sampler),
	}
	for _, processor := range processors {
		providerOpts = append(providerOpts, trace.WithSpanProcessor(processor))
	}

	return trace.NewTracerProvider(providerOpts...), nil
}

// buildSpanProcessors creates batch span processors for all exporters.
// When SpanFilter or TailSamplingEnabled are set, each exporter is wrapped
// in a chain: baggage -> filter -> tail -> batch.
//
// The baggage stage sits outermost because it writes attributes in OnStart,
// which has to happen before any stage that inspects them.
func buildSpanProcessors(exporters []trace.SpanExporter, cfg *Config, tailPolicy processors.TailPolicy) []trace.SpanProcessor {
	out := make([]trace.SpanProcessor, 0, len(exporters)+len(cfg.SpanProcessors))
	out = append(out, cfg.SpanProcessors...)

	for _, exp := range exporters {
		p := trace.NewBatchSpanProcessor(exp,
			trace.WithBatchTimeout(cfg.BatchTimeout),
			trace.WithMaxQueueSize(cfg.MaxQueueSize),
			trace.WithMaxExportBatchSize(cfg.MaxExportBatchSize),
		)
		if cfg.TailSamplingEnabled || tailPolicy != nil {
			var opts []processors.TailSamplingOption
			if tailPolicy != nil {
				opts = append(opts, processors.WithTailPolicy(tailPolicy))
			}
			p = processors.NewTailSamplingSpanProcessor(p, opts...)
		}
		if cfg.SpanFilter != nil {
			p = processors.NewFilteringSpanProcessor(cfg.SpanFilter, p)
		}
		if cfg.BaggageToAttributes {
			p = processors.NewBaggageSpanProcessor(p, cfg.BaggageSpanProcOpts...)
		}
		out = append(out, p)
	}
	return out
}

// endPolicyFor returns the tail policy implied by the configured sampler, or nil
// when every decision it makes can be made at span start.
//
// Keeping all errors is not something a head sampler can do: a span's status and
// duration do not exist when it starts. Those rates therefore travel to a span
// processor that runs at OnEnd, where the facts are in.
//
// A debug session that keeps everything has no tail policy: selectSampler keeps
// every span there, and a tail applying a baseline would drop most of them again.
func endPolicyFor(cfg *Config) processors.TailPolicy {
	if debugKeepsEverySpan(cfg) {
		return nil
	}
	adaptive, ok := cfg.Sampler.(*sampling.AdaptiveSampler)
	if !ok {
		return nil
	}
	policy := adaptive.EndPolicy()
	if !policy.Active() {
		return nil
	}
	// The keeper is stateful: it remembers which traces kept a failed span so the
	// parents that end afterwards are kept with it, rather than leaving the error
	// as an orphan with no waterfall around it.
	return policy.NewKeeper().KeepSpan
}

// monitoredExporter watches how each export goes, for two listeners the batch
// processor leaves deaf by dropping export errors: the circuit breaker, which
// would never open, and Shutdown, which would report success for a traces-only
// setup that delivered nothing.
type monitoredExporter struct {
	trace.SpanExporter
	breaker *circuitbreaker.CircuitBreaker // nil without WithCircuitBreaker
	failure *atomic.Pointer[error]         // the latest export's error, nil once one works
}

func (e *monitoredExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.failure.Store(&err)
	} else {
		e.failure.Store(nil)
	}
	if e.breaker != nil {
		if err != nil {
			e.breaker.RecordFailure()
		} else {
			e.breaker.RecordSuccess()
		}
	}
	return err
}

// guardSampler sheds load before a span is built, by asking the configured rate
// limiter and circuit breaker whether there is room for another trace.
//
// This sits in the sampler rather than in autotel.Start because every span
// reaches a sampler, whoever created it. When the checks lived in Start they
// covered only spans from Start, so a service using middleware, messaging or any
// third-party instrumentation had a limit that bounded almost nothing.
//
// Only spans starting a trace are counted. A span whose parent is already sampled
// passes through, because shedding it would leave a hole in a trace that is
// otherwise being kept, and a trace with its middle missing is worth less under
// load than no trace at all.
type guardSampler struct {
	next   trace.Sampler
	guards []interface{ Allow() bool }
}

func (g guardSampler) ShouldSample(p trace.SamplingParameters) trace.SamplingResult {
	if oteltrace.SpanContextFromContext(p.ParentContext).IsSampled() {
		return g.next.ShouldSample(p)
	}
	for _, guard := range g.guards {
		if !guard.Allow() {
			return trace.SamplingResult{Decision: trace.Drop}
		}
	}
	return g.next.ShouldSample(p)
}

func (g guardSampler) Description() string {
	return "guardSampler(" + g.next.Description() + ")"
}

// withGuards wraps sampler with the rate limiter and circuit breaker, if either
// is configured.
func withGuards(sampler trace.Sampler, cfg *Config) trace.Sampler {
	var guards []interface{ Allow() bool }
	if cfg.RateLimiter != nil {
		guards = append(guards, cfg.RateLimiter)
	}
	if cfg.CircuitBreaker != nil {
		guards = append(guards, cfg.CircuitBreaker)
	}
	if len(guards) == 0 {
		return sampler
	}
	return guardSampler{next: sampler, guards: guards}
}

// debugKeepsEverySpan reports whether debug mode overrules the sampler. Debug is
// on by default outside production, so that default must not silently cancel a
// sampler someone chose; WithDebug(true) or AUTOTEL_DEBUG does overrule it.
func debugKeepsEverySpan(cfg *Config) bool {
	return IsDebugEnabled() && (cfg.debugRequested || !cfg.samplerChosen)
}

// selectSampler returns the appropriate sampler based on config and debug mode.
//
// When a tail policy is in play the head must record every span, because a span
// dropped at head never reaches OnEnd and its error can no longer be kept. The
// baseline is not lost: the tail applies it, deterministically per trace ID. The
// cost is that spans are built in-process before being dropped; export volume
// still follows the configured rates.
func selectSampler(cfg *Config, hasTailPolicy bool) trace.Sampler {
	if debugKeepsEverySpan(cfg) {
		debugPrint("Using AlwaysSample sampler for debug mode")
		return trace.AlwaysSample()
	}
	if hasTailPolicy {
		debugPrint("Head sampling everything; the tail policy applies the configured rates")
		return trace.AlwaysSample()
	}
	return cfg.Sampler
}

// setupGlobalFeatures configures the globals that are consulted outside the
// tracer provider. The rate limiter and circuit breaker are not among them: they
// run inside the sampler, so buildTracerProvider owns them.
func setupGlobalFeatures(cfg *Config) EventTracker {
	// A typed nil would read as a redactor, hence the branch.
	if cfg.PIIRedactor != nil {
		setGlobalPIIRedactor(cfg.PIIRedactor)
	} else {
		setGlobalPIIRedactor(nil)
	}

	if len(cfg.Subscribers) == 0 || queueFactory == nil {
		return nil
	}
	tracker := queueFactory(cfg, cfg.Subscribers)
	if cfg.PIIRedactor != nil {
		tracker = &redactingTracker{EventTracker: tracker, redactor: cfg.PIIRedactor}
	}
	globalTrackerMu.Lock()
	globalTracker = tracker
	globalTrackerMu.Unlock()
	return tracker
}

// createCleanupFunc creates the cleanup function for graceful shutdown, and
// makes these providers the ones Flush and Shutdown act on.
//
// Shutting each provider down flushes what its batch processor or periodic
// reader holds. mp and lp are nil when their signal is disabled.
func createCleanupFunc(tp *trace.TracerProvider, mp *sdkmetric.MeterProvider, lp *sdklog.LoggerProvider, file *rotatefile.Writer, tracker EventTracker, spanExportFailure *atomic.Pointer[error]) func() {
	p := &pipeline{tp: tp, mp: mp, lp: lp, file: file, tracker: tracker, spanExportFailure: spanExportFailure}
	current.Store(p)

	return func() { _ = p.shutdown(context.Background()) }
}

var warnNoSubscribers sync.Once

// currentTracker returns the event queue Init built, or nil when Init was
// given no subscribers. The nil case warns once, so a missing
// WithSubscribers shows up on the first Track.
func currentTracker() EventTracker {
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

	if tracker == nil {
		warnNoSubscribers.Do(func() {
			slog.Warn("[autotel] Track called but Init was given no subscribers; events are dropped. Pass WithSubscribers(...) to Init.")
		})
	}

	return tracker
}

// Track sends an analytics event to the global queue (if configured).
// This is a convenience function that uses the queue created during Init().
// If no subscribers were provided during Init(), this function does nothing.
//
// Example:
//
//	autotel.Track(ctx, "user_signed_up", map[string]any{
//	    "user_id": "123",
//	    "plan":    "premium",
//	})
func Track(ctx context.Context, event string, properties map[string]any) {
	tracker := currentTracker()

	if tracker != nil {
		tracker.Track(ctx, event, properties)
	}
}

// TrackFunnelStep tracks a funnel step event with a predefined status.
// The event is automatically enriched with trace_id and span_id if a span is active.
//
// Example:
//
//	autotel.TrackFunnelStep(ctx, "checkout", autotel.FunnelStarted, map[string]any{
//	    "user_id": userID,
//	})
func TrackFunnelStep(ctx context.Context, funnelName string, step FunnelStatus, properties map[string]any) {
	tracker := currentTracker()

	if tracker == nil {
		return
	}

	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}
	props["funnel_name"] = funnelName
	props["funnel_status"] = string(step)

	eventName := "funnel." + funnelName + "." + string(step)
	tracker.Track(ctx, eventName, props)
}

// TrackFunnelProgression tracks progression through a multi-step funnel with custom step names.
// The event is automatically enriched with trace_id and span_id if a span is active.
//
// Example:
//
//	autotel.TrackFunnelProgression(ctx, "onboarding", "verify_email", 2, map[string]any{
//	    "user_id": userID,
//	})
func TrackFunnelProgression(ctx context.Context, funnelName string, stepName string, stepNumber int, properties map[string]any) {
	tracker := currentTracker()

	if tracker == nil {
		return
	}

	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}
	props["funnel_name"] = funnelName
	props["step_name"] = stepName
	props["step_number"] = stepNumber

	eventName := "funnel." + funnelName + ".step"
	tracker.Track(ctx, eventName, props)
}

// TrackOutcome tracks the outcome of an operation.
// The event is automatically enriched with trace_id and span_id if a span is active.
//
// Example:
//
//	autotel.TrackOutcome(ctx, "payment_processing", autotel.OutcomeSuccess, map[string]any{
//	    "amount": 99.99,
//	})
func TrackOutcome(ctx context.Context, operationName string, outcome OutcomeStatus, properties map[string]any) {
	tracker := currentTracker()

	if tracker == nil {
		return
	}

	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}
	props["operation_name"] = operationName
	props["outcome"] = string(outcome)

	eventName := "outcome." + operationName
	tracker.Track(ctx, eventName, props)
}

// TrackValue tracks a numeric value event.
// The event is automatically enriched with trace_id and span_id if a span is active.
//
// Example:
//
//	autotel.TrackValue(ctx, "order_total", 149.99, map[string]any{
//	    "currency": "USD",
//	})
func TrackValue(ctx context.Context, name string, value float64, properties map[string]any) {
	tracker := currentTracker()

	if tracker == nil {
		return
	}

	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}
	props["value"] = value

	eventName := "value." + name
	tracker.Track(ctx, eventName, props)
}

// TrackBatch tracks multiple events at once.
// Each event is automatically enriched with trace_id and span_id if a span is active.
//
// Example:
//
//	autotel.TrackBatch(ctx, []autotel.Event{
//	    {Name: "page_view", Properties: map[string]any{"page": "/home"}},
//	    {Name: "button_click", Properties: map[string]any{"button": "signup"}},
//	})
func TrackBatch(ctx context.Context, events []Event) {
	tracker := currentTracker()

	if tracker == nil {
		return
	}

	for _, event := range events {
		tracker.Track(ctx, event.Name, event.Properties)
	}
}

func setupMetrics(ctx context.Context, res *resource.Resource, cfg *Config) (*sdkmetric.MeterProvider, error) {
	if !cfg.MetricsEnabled || cfg.signals.metrics.Disabled {
		return nil, nil
	}

	exportersList := cfg.MetricExporters
	if len(exportersList) == 0 {
		if cfg.signalEndpoint(cfg.signals.metrics, metricsPath) == "" {
			// No exporter configured and no endpoint provided; skip metrics setup.
			return nil, nil
		}

		exp, err := newOTLPMetricsExporter(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
		}
		exportersList = append(exportersList, exp)
	}

	providerOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, exp := range exportersList {
		providerOpts = append(providerOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.MetricInterval))))
	}

	mp := sdkmetric.NewMeterProvider(providerOpts...)
	if cfg.RuntimeMetrics {
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			shutdownPartial(mp, nil)
			return nil, fmt.Errorf("failed to start runtime metrics: %w", err)
		}
	}
	return mp, nil
}

func newOTLPMetricsExporter(ctx context.Context, cfg *Config) (sdkmetric.Exporter, error) {
	target := cfg.signalEndpoint(cfg.signals.metrics, metricsPath)
	headers := cfg.signalHeaders(cfg.signals.metrics)
	timeout := cfg.BatchTimeout + 5*time.Second
	if cfg.Protocol == ProtocolHTTP {
		return otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(target),
			otlpmetrichttp.WithHeaders(headers),
			otlpmetrichttp.WithTimeout(timeout),
		)
	}
	return otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(target),
		otlpmetricgrpc.WithHeaders(headers),
		otlpmetricgrpc.WithTimeout(timeout),
	)
}

// buildExporters builds the list of span exporters respecting presets and custom exporters.
func buildExporters(ctx context.Context, cfg *Config) ([]trace.SpanExporter, error) {
	exportersList := make([]trace.SpanExporter, 0, 4)
	exportersList = append(exportersList, cfg.SpanExporters...)

	// Base OTLP exporter (only when endpoint configured)
	if hasOTLPTraces(cfg) {
		exp, err := newOTLPExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		exportersList = append(exportersList, exp)
	}

	// Before the console exporter joins: it can't fail, and a working printer
	// mustn't report a dead backend as healthy.
	cfg.spanExportFailure = &atomic.Pointer[error]{}
	for i, exporter := range exportersList {
		exportersList[i] = &monitoredExporter{SpanExporter: exporter, breaker: cfg.CircuitBreaker, failure: cfg.spanExportFailure}
	}

	// Console exporter for debug ergonomics
	if IsDebugEnabled() {
		exportersList = append(exportersList, exporters.NewConsoleExporter())
	}

	if cfg.PIIRedactor != nil {
		for i, exporter := range exportersList {
			exportersList[i] = &redactingExporter{SpanExporter: exporter, redactor: cfg.PIIRedactor}
		}
	}

	return exportersList, nil
}

// Per-signal OTLP paths appended to a base endpoint URL.
const (
	tracesPath  = "/v1/traces"
	metricsPath = "/v1/metrics"
	logsPath    = "/v1/logs"
)

// hasOTLPTraces reports whether traces go to an OTLP endpoint.
func hasOTLPTraces(cfg *Config) bool {
	return !cfg.signals.traces.Disabled && cfg.signalEndpoint(cfg.signals.traces, tracesPath) != ""
}

// signalEndpoint is the full URL one signal goes to: its own endpoint if the
// environment gave it one, otherwise the base endpoint with the signal's path.
//
// It is always a URL, for every exporter. The exporters read the OTEL_* variables
// themselves as defaults, and an option only replaces what it names: given a bare
// host, they would keep the scheme and path of an http:// variable, sending
// credentials meant for TLS in cleartext. A URL replaces all three at once.
func (c *Config) signalEndpoint(signal signalEnv, path string) string {
	if signal.Endpoint != "" {
		if endpointIsURL(signal.Endpoint) {
			return signal.Endpoint
		}
		return signalEndpointURL(c.withScheme(signal.Endpoint), path)
	}
	if c.Endpoint == "" {
		return ""
	}
	return signalEndpointURL(c.withScheme(c.Endpoint), path)
}

// withScheme turns a bare host:port into a URL, as Insecure says.
func (c *Config) withScheme(endpoint string) string {
	switch {
	case endpointIsURL(endpoint):
		return endpoint
	case c.Insecure:
		return "http://" + endpoint
	}
	return "https://" + endpoint
}

// signalHeaders is the base headers with the signal's own laid over them.
func (c *Config) signalHeaders(signal signalEnv) map[string]string {
	if len(signal.Headers) == 0 {
		return c.Headers
	}
	headers := make(map[string]string, len(c.Headers)+len(signal.Headers))
	maps.Copy(headers, c.Headers)
	maps.Copy(headers, signal.Headers)
	return headers
}

// endpointIsURL reports whether the endpoint is a URL rather than a bare
// host:port. sanitizeEndpoint has lowered the scheme by this point.
func endpointIsURL(endpoint string) bool {
	return strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://")
}

// signalEndpointURL joins a base endpoint URL with a per-signal OTLP path.
//
// Config carries one Endpoint for every signal, which makes it the base endpoint
// in OTLP terms, so the signal path belongs on the end of it. WithEndpointURL
// takes the path as complete and does not append anything, so a base carrying a
// path of its own — Langfuse's /api/public/otel, PostHog's /i, a Grafana gateway
// path — would otherwise have traces posted to the base itself.
//
// A base with no path gets the signal path too, so the exporter posts to
// /v1/traces whatever its own default.
func signalEndpointURL(base, signalPath string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	// Respect an endpoint that already names the signal explicitly.
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), signalPath) {
		return base
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + signalPath
	return parsed.String()
}

func newOTLPExporter(ctx context.Context, cfg *Config) (trace.SpanExporter, error) {
	target := cfg.signalEndpoint(cfg.signals.traces, tracesPath)
	headers := cfg.signalHeaders(cfg.signals.traces)
	timeout := cfg.BatchTimeout + 5*time.Second
	if cfg.Protocol == ProtocolHTTP {
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(target),
			otlptracehttp.WithHeaders(headers),
			otlptracehttp.WithTimeout(timeout),
		)
	}
	return otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(target),
		otlptracegrpc.WithHeaders(headers),
		otlptracegrpc.WithTimeout(timeout),
	)
}

// applyBackendPreset adjusts config for common vendors while staying OTLP-first.
// presetProtocol applies a vendor's protocol unless an option named one.
func presetProtocol(cfg *Config, protocol Protocol) {
	if !cfg.protocolChosen {
		cfg.Protocol = protocol
	}
}

// presetHeader adds a vendor header unless the configuration already has it.
func presetHeader(cfg *Config, key, value string) {
	if value == "" {
		return
	}
	for existing := range cfg.Headers {
		if strings.EqualFold(existing, key) {
			return
		}
	}
	cfg.Headers[key] = value
}

func applyBackendPreset(cfg *Config) {
	switch strings.ToLower(cfg.BackendPreset) {
	case "datadog", "dd":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "api.datadoghq.com:443"
		}
		presetProtocol(cfg, ProtocolGRPC)
		cfg.Insecure = false
		ensureHeaders(cfg)
		presetHeader(cfg, "DD-API-KEY", os.Getenv("DD_API_KEY"))
		cfg.Headers["X-Datadog-Origin"] = "otlp"
	case "honeycomb", "hny":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "api.honeycomb.io:443"
		}
		presetProtocol(cfg, ProtocolHTTP)
		cfg.Insecure = false
		ensureHeaders(cfg)
		presetHeader(cfg, "x-honeycomb-team", os.Getenv("HONEYCOMB_API_KEY"))
		presetHeader(cfg, "x-honeycomb-dataset", os.Getenv("HONEYCOMB_DATASET"))
	case "grafana", "grafana-cloud", "grafana_cloud":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "otlp-gateway-prod.grafana.net:443"
		}
		presetProtocol(cfg, ProtocolGRPC)
		cfg.Insecure = false
		ensureHeaders(cfg)
		if key := os.Getenv("GRAFANA_OTLP_API_KEY"); key != "" {
			presetHeader(cfg, "Authorization", "Bearer "+key)
		}
	default:
		// OTLP defaults already set
	}
}

func ensureHeaders(cfg *Config) {
	if cfg.Headers == nil {
		cfg.Headers = make(map[string]string)
	}
}
