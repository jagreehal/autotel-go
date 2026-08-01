package autotel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlpmetrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"github.com/jagreehal/autotel-go/v2/internal/exporters"
	"github.com/jagreehal/autotel-go/v2/processors"
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
	setupDebugMode(mergedCfg)

	res, err := buildResource(ctx, mergedCfg)
	if err != nil {
		return nil, err
	}

	tp, err := buildTracerProvider(ctx, res, mergedCfg)
	if err != nil {
		return nil, err
	}

	otel.SetTracerProvider(tp)
	setupGlobalFeatures(mergedCfg)

	return createCleanupFunc(tp), nil
}

// resolveAndMergeConfig resolves configs from all sources and merges them.
func resolveAndMergeConfig(explicit *Config) *Config {
	envCfg := resolveEnvConfig()

	var yamlCfg *Config
	if yamlConfig, err := loadYamlConfigFromAutotel(); err == nil && yamlConfig != nil {
		yamlCfg = yamlConfig
	}

	merged := mergeConfigs(explicit, yamlCfg, envCfg)
	applyBackendPreset(merged)
	return merged
}

// setupDebugMode configures debug mode based on config.
func setupDebugMode(cfg *Config) {
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
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.DeploymentEnvironment(cfg.Environment),
	}

	for k, v := range cfg.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(),
		resource.WithProcess(),
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

	if err := setupMetrics(ctx, res, cfg); err != nil {
		return nil, err
	}

	processors := buildSpanProcessors(exportersList, cfg)
	sampler := selectSampler(cfg)

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
// When SpanFilter, SpanNameNormalizer, AttributeRedactor, or TailSamplingEnabled are set,
// each exporter is wrapped in a chain: filter -> normalizer -> redactor -> tail -> batch.
func buildSpanProcessors(exporters []trace.SpanExporter, cfg *Config) []trace.SpanProcessor {
	out := make([]trace.SpanProcessor, 0, len(exporters)+len(cfg.SpanProcessors))
	out = append(out, cfg.SpanProcessors...)

	for _, exp := range exporters {
		p := trace.NewBatchSpanProcessor(exp,
			trace.WithBatchTimeout(cfg.BatchTimeout),
			trace.WithMaxQueueSize(cfg.MaxQueueSize),
			trace.WithMaxExportBatchSize(cfg.MaxExportBatchSize),
		)
		if cfg.TailSamplingEnabled {
			p = processors.NewTailSamplingSpanProcessor(p)
		}
		if cfg.AttributeRedactor != nil {
			p = processors.NewAttributeRedactingProcessor(cfg.AttributeRedactor, p)
		}
		if cfg.SpanNameNormalizer != nil {
			p = processors.NewSpanNameNormalizingProcessor(cfg.SpanNameNormalizer, p)
		}
		if cfg.SpanFilter != nil {
			p = processors.NewFilteringSpanProcessor(cfg.SpanFilter, p)
		}
		out = append(out, p)
	}
	return out
}

// selectSampler returns the appropriate sampler based on config and debug mode.
func selectSampler(cfg *Config) trace.Sampler {
	if IsDebugEnabled() {
		debugPrint("Using AlwaysSample sampler for debug mode")
		return trace.AlwaysSample()
	}
	return cfg.Sampler
}

// setupGlobalFeatures configures global rate limiters, circuit breakers, etc.
func setupGlobalFeatures(cfg *Config) {
	if cfg.RateLimiter != nil {
		setGlobalRateLimiter(cfg.RateLimiter)
	}
	if cfg.CircuitBreaker != nil {
		setGlobalCircuitBreaker(cfg.CircuitBreaker)
	}
	if cfg.PIIRedactor != nil {
		setGlobalPIIRedactor(cfg.PIIRedactor)
	}

	if len(cfg.Subscribers) > 0 && queueFactory != nil {
		globalTrackerMu.Lock()
		globalTracker = queueFactory(cfg, cfg.Subscribers)
		globalTrackerMu.Unlock()
	}
}

// createCleanupFunc creates the cleanup function for graceful shutdown.
func createCleanupFunc(tp *trace.TracerProvider) func() {
	return func() {
		_ = tp.Shutdown(context.Background())

		globalTrackerMu.Lock()
		if globalTracker != nil {
			_ = globalTracker.Shutdown(context.Background())
			globalTracker = nil
		}
		globalTrackerMu.Unlock()
	}
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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

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
	globalTrackerMu.RLock()
	tracker := globalTracker
	globalTrackerMu.RUnlock()

	if tracker == nil {
		return
	}

	for _, event := range events {
		tracker.Track(ctx, event.Name, event.Properties)
	}
}

func setupMetrics(ctx context.Context, res *resource.Resource, cfg *Config) error {
	if !cfg.MetricsEnabled {
		return nil
	}

	exportersList := cfg.MetricExporters
	if len(exportersList) == 0 {
		if cfg.Endpoint == "" {
			// No exporter configured and no endpoint provided; skip metrics setup.
			return nil
		}

		exp, err := newOTLPMetricsExporter(ctx, cfg)
		if err != nil {
			return fmt.Errorf("failed to create metrics exporter: %w", err)
		}
		exportersList = append(exportersList, exp)
	}

	providerOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, exp := range exportersList {
		providerOpts = append(providerOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.MetricInterval))))
	}

	mp := sdkmetric.NewMeterProvider(providerOpts...)
	otel.SetMeterProvider(mp)
	return nil
}

func newOTLPMetricsExporter(ctx context.Context, cfg *Config) (sdkmetric.Exporter, error) {
	if cfg.Protocol == ProtocolHTTP {
		httpOpts := []otlptmetricOption{
			otlptmetricWithEndpoint(cfg.Endpoint),
			otlptmetricWithHeaders(cfg.Headers),
			otlptmetricWithTimeout(cfg.BatchTimeout + 5*time.Second),
		}
		if cfg.Insecure {
			httpOpts = append(httpOpts, otlptmetricWithInsecure())
		}
		return otlpmetrichttp.New(ctx, httpOpts...)
	}

	grpcOpts := []otlpgmetricOption{
		otlpgmetricWithEndpoint(cfg.Endpoint),
		otlpgmetricWithHeaders(cfg.Headers),
		otlpgmetricWithTimeout(cfg.BatchTimeout + 5*time.Second),
	}
	if cfg.Insecure {
		grpcOpts = append(grpcOpts, otlpmetricgrpc.WithInsecure())
	}
	return otlpmetricgrpc.New(ctx, grpcOpts...)
}

// Aliases to keep option slices readable.
type (
	otlptmetricOption = otlpmetrichttp.Option
	otlpgmetricOption = otlpmetricgrpc.Option
)

func otlptmetricWithEndpoint(e string) otlptmetricOption { return otlpmetrichttp.WithEndpoint(e) }
func otlptmetricWithHeaders(h map[string]string) otlptmetricOption {
	return otlpmetrichttp.WithHeaders(h)
}
func otlptmetricWithTimeout(d time.Duration) otlptmetricOption { return otlpmetrichttp.WithTimeout(d) }
func otlptmetricWithInsecure() otlptmetricOption               { return otlpmetrichttp.WithInsecure() }

func otlpgmetricWithEndpoint(e string) otlpgmetricOption { return otlpmetricgrpc.WithEndpoint(e) }
func otlpgmetricWithHeaders(h map[string]string) otlpgmetricOption {
	return otlpmetricgrpc.WithHeaders(h)
}
func otlpgmetricWithTimeout(d time.Duration) otlpgmetricOption { return otlpmetricgrpc.WithTimeout(d) }

// buildExporters builds the list of span exporters respecting presets and custom exporters.
func buildExporters(ctx context.Context, cfg *Config) ([]trace.SpanExporter, error) {
	exportersList := make([]trace.SpanExporter, 0, 4)
	exportersList = append(exportersList, cfg.SpanExporters...)

	// Base OTLP exporter (only when endpoint configured)
	if cfg.Endpoint != "" {
		exp, err := newOTLPExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		exportersList = append(exportersList, exp)
	}

	// Console exporter for debug ergonomics
	if IsDebugEnabled() {
		exportersList = append(exportersList, exporters.NewConsoleExporter())
	}

	return exportersList, nil
}

func newOTLPExporter(ctx context.Context, cfg *Config) (trace.SpanExporter, error) {
	if cfg.Protocol == ProtocolHTTP {
		httpOpts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithHeaders(cfg.Headers),
			otlptracehttp.WithTimeout(cfg.BatchTimeout + 5*time.Second),
		}
		if cfg.Insecure {
			httpOpts = append(httpOpts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, httpOpts...)
	}

	grpcOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithHeaders(cfg.Headers),
		otlptracegrpc.WithTimeout(cfg.BatchTimeout + 5*time.Second),
	}
	if cfg.Insecure {
		grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, grpcOpts...)
}

// applyBackendPreset adjusts config for common vendors while staying OTLP-first.
func applyBackendPreset(cfg *Config) {
	switch strings.ToLower(cfg.BackendPreset) {
	case "datadog", "dd":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "api.datadoghq.com:443"
		}
		cfg.Protocol = ProtocolGRPC
		cfg.Insecure = false
		ensureHeaders(cfg)
		if key := os.Getenv("DD_API_KEY"); key != "" {
			cfg.Headers["DD-API-KEY"] = key
		}
		cfg.Headers["X-Datadog-Origin"] = "otlp"
	case "honeycomb", "hny":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "api.honeycomb.io:443"
		}
		cfg.Protocol = ProtocolHTTP
		cfg.Insecure = false
		ensureHeaders(cfg)
		if key := os.Getenv("HONEYCOMB_API_KEY"); key != "" {
			cfg.Headers["x-honeycomb-team"] = key
		}
		if dataset := os.Getenv("HONEYCOMB_DATASET"); dataset != "" {
			cfg.Headers["x-honeycomb-dataset"] = dataset
		}
	case "grafana", "grafana-cloud", "grafana_cloud":
		if cfg.Endpoint == "" || cfg.Endpoint == "localhost:4318" {
			cfg.Endpoint = "otlp-gateway-prod.grafana.net:443"
		}
		cfg.Protocol = ProtocolGRPC
		cfg.Insecure = false
		ensureHeaders(cfg)
		if key := os.Getenv("GRAFANA_OTLP_API_KEY"); key != "" {
			cfg.Headers["Authorization"] = "Bearer " + key
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
