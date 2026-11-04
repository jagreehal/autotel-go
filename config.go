package autotel

import (
	"sync/atomic"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2/circuitbreaker"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/ratelimit"
	"github.com/jagreehal/autotel-go/v2/redaction"
)

// Protocol defines the OTLP protocol to use
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolGRPC Protocol = "grpc"
)

const (
	defaultServiceName    = "unknown-service"
	defaultServiceVersion = "0.0.0"
	defaultEnvironment    = "development"
)

// Config holds autotel configuration.
//
// Most users should use the functional options pattern with Init():
//
//	cleanup, err := autotel.Init(ctx,
//	    autotel.WithService("my-service"),
//	    autotel.WithEndpoint("localhost:4318"),
//	)
//
// Advanced users can create and modify Config directly for more control:
//
//	cfg := autotel.DefaultConfig()
//	cfg.ServiceName = "my-service"
//	cfg.Endpoint = "custom:4318"
//	// ... customize further
//	cleanup, err := autotel.InitWithConfig(ctx, cfg)
type Config struct {
	// ServiceName is the name of your service (required)
	ServiceName string

	// ServiceVersion is the version of your service (optional)
	ServiceVersion string

	// Environment is the deployment environment (e.g., "production", "staging")
	Environment string

	// Endpoint is the OTLP endpoint URL (default: "localhost:4318")
	Endpoint string

	// Protocol is the OTLP protocol to use (http or grpc)
	Protocol Protocol

	// Insecure controls whether to use insecure connections (default: true for development)
	Insecure bool

	// Sampler is the trace sampler to use (default: keep every trace, following
	// the caller's decision when there is one)
	Sampler trace.Sampler

	// Production hardening features

	// RateLimiter limits span creation rate (optional)
	RateLimiter *ratelimit.TokenBucket

	// CircuitBreaker protects exporter from overload (optional)
	CircuitBreaker *circuitbreaker.CircuitBreaker

	// PIIRedactor redacts PII from span attributes (optional)
	PIIRedactor *redaction.PIIRedactor

	// UseAdaptiveSampler indicates whether adaptive sampling is enabled
	UseAdaptiveSampler bool

	// samplerChosen records that an option picked the sampler, so a default
	// that depends on the sampler (WithDevtools keeping every trace) can tell
	// an explicit adaptive sampler from the default one.
	samplerChosen bool

	// Subscribers are event subscribers (PostHog, Mixpanel, etc.)
	// If provided, a global event queue will be created automatically.
	Subscribers []Subscriber

	// BackendPreset enables vendor presets (datadog, honeycomb, grafana, default otlp).
	BackendPreset string

	// Headers are additional headers sent to the exporter (API keys, datasets, etc.).
	Headers map[string]string

	// ResourceAttributes are additional resource attributes to attach to all traces/metrics.
	ResourceAttributes map[string]string

	// Additional span exporters beyond the default OTLP exporter.
	SpanExporters []trace.SpanExporter

	// Additional span processors to attach.
	SpanProcessors []trace.SpanProcessor

	// Optional pipeline: span filter and tail sampling.
	// When set, these wrap each batch exporter in order: filter -> tail -> batch.
	SpanFilter          processors.SpanFilterPredicate
	TailSamplingEnabled bool

	// DebugCapture honours DebugBaggageKey on incoming requests. Configure
	// with WithDebugCapture.
	DebugCapture bool

	// Devtools sends everything to a local autotel-devtools receiver when no
	// endpoint is configured. Configure with WithDevtools.
	Devtools bool

	// BaggageToAttributes copies baggage onto span attributes when set.
	// Configure with WithBaggageAttributes.
	BaggageToAttributes bool
	BaggageSpanProcOpts []processors.BaggageSpanProcessorOption

	// Event queue tuning.
	EventQueueSize     int
	EventFlushInterval time.Duration
	EventCBThreshold   int
	EventBackoffMin    time.Duration
	EventBackoffMax    time.Duration
	EventCBReset       time.Duration
	EventMaxRetries    int
	EventJitter        time.Duration

	// Batch processor tuning knobs (match TS README).
	BatchTimeout       time.Duration
	MaxQueueSize       int
	MaxExportBatchSize int

	// Debug allows auto detection when nil.
	Debug *bool

	// Metrics control.
	MetricsEnabled  bool
	MetricExporters []metric.Exporter
	MetricInterval  time.Duration

	// RuntimeMetrics reports Go runtime metrics (memory, GC, goroutines).
	RuntimeMetrics bool

	// Logs control. OTEL_LOGS_EXPORTER=none also disables log export.
	LogsEnabled  bool
	LogExporters []sdklog.Exporter

	// LogFile also writes Logger output to rolling files.
	LogFile *LogFile

	// These record that a value was chosen, which matters when it equals the
	// default: a default gives way to lower layers, a choice doesn't.
	protocolChosen    bool
	versionChosen     bool
	environmentChosen bool

	// Bad values from the environment, which matter only if nothing outranks them.
	protocolEnvErr error
	samplerEnvErr  error

	// spanExportFailure is where span exporters leave their latest error.
	spanExportFailure *atomic.Pointer[error]

	// signals holds per-signal settings that only the environment can set.
	signals struct{ traces, metrics, logs signalEnv }

	// debugRequested records that debug mode was asked for, rather than being on
	// because ENV isn't production. Only a requested debug overrules a sampler.
	debugRequested bool

	// optionErrors collects validation failures raised by options, so a bad
	// vendor preset surfaces as an Init error rather than panicking or silently
	// exporting nowhere. Append via OptionError.
	optionErrors []error
}

// signalEnv is what OTEL_* variables say about one signal.
type signalEnv struct {
	Endpoint string // used verbatim: no signal path is appended
	Headers  map[string]string
	Disabled bool // OTEL_{TRACES,METRICS,LOGS}_EXPORTER=none
}

// fillZeroValues gives a hand-built Config working numbers. A zero batch timeout
// or queue size builds a processor that spins and exports nothing. The booleans
// can't be told from a choice, so start from DefaultConfig to get those.
func fillZeroValues(cfg *Config) {
	defaults := defaultConfig()
	durations := []struct{ value, fallback *time.Duration }{
		{&cfg.BatchTimeout, &defaults.BatchTimeout},
		{&cfg.MetricInterval, &defaults.MetricInterval},
		{&cfg.EventFlushInterval, &defaults.EventFlushInterval},
		{&cfg.EventBackoffMin, &defaults.EventBackoffMin},
		{&cfg.EventBackoffMax, &defaults.EventBackoffMax},
		{&cfg.EventCBReset, &defaults.EventCBReset},
	}
	for _, d := range durations {
		if *d.value <= 0 {
			*d.value = *d.fallback
		}
	}
	counts := []struct{ value, fallback *int }{
		{&cfg.MaxQueueSize, &defaults.MaxQueueSize},
		{&cfg.MaxExportBatchSize, &defaults.MaxExportBatchSize},
		{&cfg.EventQueueSize, &defaults.EventQueueSize},
		{&cfg.EventCBThreshold, &defaults.EventCBThreshold},
	}
	for _, c := range counts {
		if *c.value <= 0 {
			*c.value = *c.fallback
		}
	}
	if cfg.Sampler == nil {
		cfg.Sampler = defaults.Sampler
	}
}

// defaultSampler keeps every trace. Dropping data is a cost decision for the
// person running the service, so sampling is opt-in: see WithAdaptiveSampler,
// WithTargetRateSampler and OTEL_TRACES_SAMPLER.
func defaultSampler() trace.Sampler {
	return trace.ParentBased(trace.AlwaysSample())
}

// OptionError records a validation failure on the config. Options that can fail
// validation — vendor presets in particular — call this instead of panicking, so
// the failure surfaces from Init with everything else.
func (c *Config) OptionError(err error) {
	if err != nil {
		c.optionErrors = append(c.optionErrors, err)
	}
}

// OptionErrors returns the validation failures accumulated by options.
func (c *Config) OptionErrors() []error {
	return c.optionErrors
}

// DefaultConfig returns a Config with sensible defaults.
// You can modify the returned Config and pass it to InitWithConfig.
//
// Example:
//
//	cfg := autotel.DefaultConfig()
//	cfg.ServiceName = "my-service"
//	cfg.Environment = "production"
//	cleanup, err := autotel.InitWithConfig(ctx, cfg)
func DefaultConfig() *Config {
	return defaultConfig()
}

func defaultConfig() *Config {
	return &Config{
		ServiceName:        defaultServiceName,
		ServiceVersion:     defaultServiceVersion,
		Environment:        defaultEnvironment,
		Endpoint:           "",
		Protocol:           ProtocolHTTP,
		Insecure:           true,
		Sampler:            defaultSampler(),
		BatchTimeout:       5 * time.Second,
		MaxQueueSize:       2048,
		MaxExportBatchSize: 512,
		EventQueueSize:     1000,
		EventFlushInterval: time.Second,
		EventCBThreshold:   5,
		EventBackoffMin:    100 * time.Millisecond,
		EventBackoffMax:    5 * time.Second,
		EventCBReset:       10 * time.Second,
		EventMaxRetries:    0, // 0 = unlimited
		EventJitter:        100 * time.Millisecond,
		MetricsEnabled:     true,
		MetricInterval:     60 * time.Second,
		LogsEnabled:        true,
	}
}
