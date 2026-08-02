package sampling

import (
	"time"

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

// WithErrorRate sets the keep rate for spans that end with an Error status
// (default 1.0 = 100%).
//
// A span's status is not known when it starts, so this is applied at OnEnd by
// the tail processor autotel.Init installs, not by ShouldSample. Building a
// TracerProvider directly from this sampler leaves it unenforced.
func WithErrorRate(rate float64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		if rate >= 0 && rate <= 1 {
			s.errorRate = rate
		}
	}
}

// WithSlowThreshold sets the slow operation threshold in nanoseconds.
//
// Duration is only known once a span ends, so this is applied at OnEnd by the
// tail processor autotel.Init installs. See WithErrorRate.
func WithSlowThreshold(thresholdNano int64) AdaptiveSamplerOption {
	return func(s *AdaptiveSampler) {
		s.slowThresholdNano = thresholdNano
	}
}

// WithSlowRate sets the keep rate for spans at or above the slow threshold
// (default 1.0 = 100%). Applied at OnEnd; see WithErrorRate.
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
//
// The sampler is one half of the configuration. The baseline and links rates are
// decided at span start and enforced here; the error and latency rates depend on
// how a span finished and are enforced by a tail processor. autotel.Init reads
// EndPolicy and wires that half automatically. Pass this sampler to a
// TracerProvider yourself and only the head half applies.
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

// EndPolicy returns the half of this sampler's configuration that can only be
// applied once a span has ended. autotel.Init uses it to install the matching
// tail processor.
func (s *AdaptiveSampler) EndPolicy() EndPolicy {
	return EndPolicy{
		BaselineRate:  s.baselineRate,
		ErrorRate:     s.errorRate,
		SlowThreshold: time.Duration(s.slowThresholdNano),
		SlowRate:      s.slowRate,
	}
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
	return keepAtRate(traceID, rate)
}

// shouldSampleBaseline uses TraceID to make deterministic sampling decision.
// This ensures all spans in a trace have the same decision.
func (s *AdaptiveSampler) shouldSampleBaseline(traceID oteltrace.TraceID) bool {
	return keepAtRate(traceID, s.baselineRate)
}

// Description returns sampler description.
func (s *AdaptiveSampler) Description() string {
	return "AdaptiveSampler"
}
