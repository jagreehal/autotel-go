package autotel

import (
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/circuitbreaker"
	"github.com/jagreehal/autotel-go/ratelimit"
	"github.com/jagreehal/autotel-go/redaction"
	"github.com/jagreehal/autotel-go/sampling"
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

	// Sampler is the trace sampler to use (default: AdaptiveSampler)
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

	// Subscribers are event subscribers (PostHog, Mixpanel, etc.)
	// If provided, a global event queue will be created automatically.
	Subscribers []Subscriber

	// BackendPreset enables vendor presets (datadog, honeycomb, grafana, default otlp).
	BackendPreset string

	// OTLPHeaders are additional headers sent to the exporter (API keys, datasets, etc.).
	OTLPHeaders map[string]string

	// ResourceAttributes are additional resource attributes to attach to all traces/metrics.
	ResourceAttributes map[string]string

	// Additional span exporters beyond the default OTLP exporter.
	SpanExporters []trace.SpanExporter

	// Additional span processors to attach.
	SpanProcessors []trace.SpanProcessor

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
		Sampler:            sampling.NewAdaptiveSampler(), // Use adaptive sampler by default
		UseAdaptiveSampler: true,
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
	}
}
