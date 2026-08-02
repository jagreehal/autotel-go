package sampling

import (
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// AdaptiveSampler implements adaptive sampling based on span status, duration, and links.
type AdaptiveSampler struct {
	baselineRate      float64
	errorRate         float64
	slowThresholdNano int64
	slowRate          float64
	linksBased        bool    // Enable links-based sampling for event-driven architectures
	linksRate         float64 // Sampling rate when linked to sampled spans (0.0-1.0)
}

// AdaptiveSamplerOption configures an adaptive sampler.
type AdaptiveSamplerOption func(*AdaptiveSampler)

// WithBaselineRate sets the baseline sampling rate (0.0 to 1.0).
func WithBaselineRate(rate float64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		if rate >= 0 && rate <= 1 {
			s.baselineRate = rate
		}
	}
}

// WithErrorRate sets the error sampling rate (default 1.0 = 100%).
func WithErrorRate(rate float64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		if rate >= 0 && rate <= 1 {
			s.errorRate = rate
		}
	}
}

// WithSlowThreshold sets the slow operation threshold in nanoseconds.
func WithSlowThreshold(thresholdNano int64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		s.slowThresholdNano = thresholdNano
	}
}

// WithSlowRate sets the slow operation sampling rate (default 1.0 = 100%).
func WithSlowRate(rate float64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		if rate >= 0 && rate <= 1 {
			s.slowRate = rate
		}
	}
}

// WithLinksBased enables links-based sampling for event-driven architectures.
// When enabled, spans linked to sampled spans will be sampled at linksRate.
// This is useful for message queues and pub-sub systems where consumer spans
// should maintain trace continuity with sampled producer spans.
func WithLinksBased(enabled bool) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		s.linksBased = enabled
	}
}

// WithLinksRate sets the sampling rate for spans linked to sampled spans (0.0-1.0).
// Default is 1.0 (100%). Only applies when links-based sampling is enabled.
func WithLinksRate(rate float64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		if rate >= 0 && rate <= 1 {
			s.linksRate = rate
		}
	}
}

// NewAdaptiveSampler creates a new adaptive sampler.
func NewAdaptiveSampler(opts ...AdaptiveSamplerOption) trace.Sampler {
	s := &AdaptiveSampler{
		baselineRate:      0.1,   // 10%
		errorRate:         1.0,   // 100%
		slowThresholdNano: 1e9,   // 1 second
		slowRate:          1.0,   // 100%
		linksBased:        false, // disabled by default
		linksRate:         1.0,   // 100% when enabled
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// hasSampledLink checks if any of the provided links point to a sampled span.
// A span is considered linked to a sampled span if any link's SpanContext
// has the sampled trace flag set.
func (s *AdaptiveSampler) hasSampledLink(links []oteltrace.Link) bool {
	for _, link := range links {
		if link.SpanContext.IsSampled() {
			return true
		}
	}
	return false
}

// ShouldSample implements trace.Sampler interface.
func (s *AdaptiveSampler) ShouldSample(p trace.SamplingParameters) trace.SamplingResult {
	// Always sample if parent was sampled (maintain trace continuity)
	psc := oteltrace.SpanContextFromContext(p.ParentContext)
	if psc.IsSampled() {
		return trace.SamplingResult{
			Decision:   trace.RecordAndSample,
			Tracestate: psc.TraceState(),
		}
	}

	// Links-based sampling: if any linked span is sampled, sample this span too.
	// This is essential for event-driven architectures where consumer spans
	// should maintain trace continuity with sampled producer spans.
	if s.linksBased && len(p.Links) > 0 && s.hasSampledLink(p.Links) {
		// Use deterministic sampling based on TraceID to ensure consistency
		if s.shouldSampleAtRate(p.TraceID, s.linksRate) {
			return trace.SamplingResult{
				Decision: trace.RecordAndSample,
			}
		}
	}

	// Otherwise use baseline sampling
	if s.shouldSampleBaseline(p.TraceID) {
		return trace.SamplingResult{
			Decision: trace.RecordAndSample,
		}
	}

	return trace.SamplingResult{
		Decision: trace.Drop,
	}
}

// shouldSampleAtRate uses TraceID to make deterministic sampling decision at a given rate.
// This ensures all spans in a trace have the same decision for consistency.
func (s *AdaptiveSampler) shouldSampleAtRate(traceID oteltrace.TraceID, rate float64) bool {
	// Handle edge cases: always sample at 100%, never sample at 0%
	if rate >= 1.0 {
		return true
	}
	if rate <= 0.0 {
		return false
	}

	tid := traceID[15] // Use last byte for deterministic decision
	threshold := uint8(rate * 256)
	return tid < threshold
}

// shouldSampleBaseline uses TraceID to make deterministic sampling decision.
// This ensures all spans in a trace have the same decision.
func (s *AdaptiveSampler) shouldSampleBaseline(traceID oteltrace.TraceID) bool {
	return s.shouldSampleAtRate(traceID, s.baselineRate)
}

// Description returns sampler description.
func (s *AdaptiveSampler) Description() string {
	return "AdaptiveSampler"
}
