package autotel

import (
	"time"

	metricSdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2/circuitbreaker"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/ratelimit"
	"github.com/jagreehal/autotel-go/v2/redaction"
	"github.com/jagreehal/autotel-go/v2/sampling"
)

// Option is a functional option for configuring autotel
type Option func(*Config)

// WithService sets the service name
func WithService(name string) Option {
	return func(c *Config) {
		c.ServiceName = name
	}
}

// WithServiceVersion sets the service version
func WithServiceVersion(version string) Option {
	return func(c *Config) {
		c.ServiceVersion = version
	}
}

// WithEnvironment sets the deployment environment
func WithEnvironment(env string) Option {
	return func(c *Config) {
		c.Environment = env
	}
}

// WithEndpoint sets the OTLP endpoint
func WithEndpoint(endpoint string) Option {
	return func(c *Config) {
		c.Endpoint = endpoint
	}
}

// WithProtocol sets the OTLP protocol (http or grpc)
func WithProtocol(protocol Protocol) Option {
	return func(c *Config) {
		c.Protocol = protocol
	}
}

// WithSampler sets a custom sampler
func WithSampler(sampler trace.Sampler) Option {
	return func(c *Config) {
		c.Sampler = sampler
		c.UseAdaptiveSampler = false
	}
}

// WithInsecure controls whether to use insecure connections
func WithInsecure(insecure bool) Option {
	return func(c *Config) {
		c.Insecure = insecure
	}
}

// WithRateLimit enables rate limiting for span creation.
// rate is the number of spans per second, burst is the maximum burst size.
func WithRateLimit(rate float64, burst int) Option {
	return func(c *Config) {
		c.RateLimiter = ratelimit.NewTokenBucket(rate, burst)
	}
}

// WithCircuitBreaker enables circuit breaker protection.
// failureThreshold is the number of failures before opening the circuit.
// successThreshold is the number of successes needed to close from half-open.
// timeout is how long to wait before attempting recovery.
func WithCircuitBreaker(failureThreshold, successThreshold int, timeout time.Duration) Option {
	return func(c *Config) {
		c.CircuitBreaker = circuitbreaker.NewCircuitBreaker(failureThreshold, successThreshold, timeout)
	}
}

// WithPIIRedaction enables PII redaction with optional configuration.
func WithPIIRedaction(opts ...redaction.PIIRedactorOption) Option {
	return func(c *Config) {
		c.PIIRedactor = redaction.NewPIIRedactor(opts...)
	}
}

// WithAdaptiveSampler configures the adaptive sampler with custom options.
// Use sampling.With* options to customize sampling behavior.
//
// Example:
//
//	autotel.Init(ctx,
//	    autotel.WithAdaptiveSampler(
//	        sampling.WithBaselineRate(0.1),
//	        sampling.WithLinksBased(true),
//	        sampling.WithLinksRate(1.0),
//	    ),
//	)
func WithAdaptiveSampler(opts ...sampling.AdaptiveSamplerOption) Option {
	return func(c *Config) {
		c.Sampler = sampling.NewAdaptiveSampler(opts...)
		c.UseAdaptiveSampler = true
	}
}

// WithLinksBasedSampling enables links-based sampling for event-driven architectures.
// When a span is linked to a sampled span (e.g., message consumer linked to producer),
// it will be sampled at the specified rate to maintain trace continuity.
//
// This is essential for message queues and pub-sub systems where consumer spans
// should be sampled when they process messages from sampled producer spans.
//
// Parameters:
//   - rate: Sampling rate for linked spans (0.0 to 1.0, default 1.0 = 100%)
//
// Example:
//
//	autotel.Init(ctx,
//	    autotel.WithLinksBasedSampling(1.0), // 100% sample when linked to sampled span
//	)
//
// Usage with message consumer:
//
//	headers := map[string]string{"traceparent": msg.Headers["traceparent"]}
//	link, ok := sampling.CreateLinkFromHeaders(headers)
//	if ok {
//	    ctx, span := tracer.Start(ctx, "process-message", trace.WithLinks(link))
//	    defer span.End()
//	}
func WithLinksBasedSampling(rate float64) Option {
	return func(c *Config) {
		c.Sampler = sampling.NewAdaptiveSampler(
			sampling.WithLinksBased(true),
			sampling.WithLinksRate(rate),
		)
		c.UseAdaptiveSampler = true
	}
}

// WithDebug enables debug mode, which logs all span operations to stderr.
func WithDebug(enabled bool) Option {
	return func(c *Config) {
		c.Debug = &enabled
	}
}

// WithSubscribers sets event subscribers.
// If provided, a global event queue will be created automatically.
// The queue will be shut down when the cleanup function from Init() is called.
//
// Example:
//
//	cleanup, err := autotel.Init(ctx,
//	    autotel.WithService("my-service"),
//	    autotel.WithSubscribers(
//	        subscribers.NewPostHogSubscriber("phc_..."),
//	    ),
//	)
//	defer cleanup()
//
//	// Use the global Track function
//	autotel.Track(ctx, "user_signed_up", map[string]any{
//	    "user_id": "123",
//	})
func WithSubscribers(subscribers ...Subscriber) Option {
	return func(c *Config) {
		c.Subscribers = subscribers
	}
}

// WithBackend enables a vendor preset ("datadog", "honeycomb", "grafana", "otlp").
// Presets remain OTLP-first and only adjust endpoints/headers.
func WithBackend(name string) Option {
	return func(c *Config) {
		c.BackendPreset = name
	}
}

// WithHeaders adds custom headers to OTLP exporters (API keys, datasets, etc.).
func WithHeaders(headers map[string]string) Option {
	return func(c *Config) {
		if c.Headers == nil {
			c.Headers = make(map[string]string)
		}
		for k, v := range headers {
			c.Headers[k] = v
		}
	}
}

// WithSpanExporters appends custom span exporters.
func WithSpanExporters(exporters ...trace.SpanExporter) Option {
	return func(c *Config) {
		c.SpanExporters = append(c.SpanExporters, exporters...)
	}
}

// WithSpanProcessors appends custom span processors.
func WithSpanProcessors(procs ...trace.SpanProcessor) Option {
	return func(c *Config) {
		c.SpanProcessors = append(c.SpanProcessors, procs...)
	}
}

// WithSpanFilter sets a predicate to drop spans for which the function returns false.
func WithSpanFilter(predicate processors.SpanFilterPredicate) Option {
	return func(c *Config) {
		c.SpanFilter = predicate
	}
}

// WithTailSampling enables tail sampling: spans with sampling.tail.evaluated=true and
// sampling.tail.keep=false are dropped. Callers must set these attributes to use tail sampling.
func WithTailSampling(enabled bool) Option {
	return func(c *Config) {
		c.TailSamplingEnabled = enabled
	}
}

// WithBatchTimeout overrides the batch processor timeout.
func WithBatchTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.BatchTimeout = timeout
	}
}

// WithMaxQueueSize overrides exporter queue size.
func WithMaxQueueSize(size int) Option {
	return func(c *Config) {
		c.MaxQueueSize = size
	}
}

// WithMaxExportBatchSize overrides exporter batch size.
func WithMaxExportBatchSize(size int) Option {
	return func(c *Config) {
		c.MaxExportBatchSize = size
	}
}

// WithEventQueue configures event queue buffer, flush interval (for retries), and breaker threshold.
func WithEventQueue(size int, flushInterval time.Duration, circuitBreakerThreshold int) Option {
	return func(c *Config) {
		c.EventQueueSize = size
		c.EventFlushInterval = flushInterval
		c.EventCBThreshold = circuitBreakerThreshold
	}
}

// WithEventBackoff configures per-subscriber backoff and circuit reset.
func WithEventBackoff(min, max, reset time.Duration) Option {
	return func(c *Config) {
		c.EventBackoffMin = min
		c.EventBackoffMax = max
		c.EventCBReset = reset
	}
}

// WithEventRetry configures max retries (0 = unlimited) and jitter.
func WithEventRetry(maxRetries int, jitter time.Duration) Option {
	return func(c *Config) {
		c.EventMaxRetries = maxRetries
		c.EventJitter = jitter
	}
}

// WithMetrics toggles metric export.
func WithMetrics(enabled bool) Option {
	return func(c *Config) {
		c.MetricsEnabled = enabled
	}
}

// WithMetricExporters appends custom metric exporters.
func WithMetricExporters(exporters ...metricSdk.Exporter) Option {
	return func(c *Config) {
		c.MetricExporters = append(c.MetricExporters, exporters...)
	}
}

// WithMetricInterval sets periodic reader interval.
func WithMetricInterval(d time.Duration) Option {
	return func(c *Config) {
		c.MetricInterval = d
	}
}
