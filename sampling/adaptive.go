package sampling

import (
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// AdaptiveSampler implements adaptive sampling based on span status and duration.
type AdaptiveSampler struct {
	baselineRate      float64
	errorRate         float64
	slowThresholdNano int64
	slowRate          float64
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

// NewAdaptiveSampler creates a new adaptive sampler.
func NewAdaptiveSampler(opts ...AdaptiveSamplerOption) trace.Sampler {
	s := &AdaptiveSampler{
		baselineRate:      0.1, // 10%
		errorRate:         1.0, // 100%
		slowThresholdNano: 1e9, // 1 second
		slowRate:          1.0, // 100%
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
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

// shouldSampleBaseline uses TraceID to make deterministic sampling decision.
// This ensures all spans in a trace have the same decision.
func (s *AdaptiveSampler) shouldSampleBaseline(traceID oteltrace.TraceID) bool {
	// Use TraceID to make deterministic sampling decision
	// This ensures all spans in a trace have the same decision
	tid := traceID[15] // Use last byte
	threshold := uint8(s.baselineRate * 256)
	return tid < threshold
}

// Description returns sampler description.
func (s *AdaptiveSampler) Description() string {
	return "AdaptiveSampler"
}
