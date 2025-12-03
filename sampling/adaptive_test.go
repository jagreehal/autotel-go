package sampling

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestAdaptiveSampler_BaselineSampling(t *testing.T) {
	sampler := NewAdaptiveSampler(WithBaselineRate(0.1)) // 10%

	// Create a context without a parent span
	ctx := context.Background()

	// Generate many trace IDs and check sampling rate
	sampled := 0
	total := 1000
	for i := 0; i < total; i++ {
		tid := oteltrace.TraceID{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i >> 32),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i >> 32),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i)}
		result := sampler.ShouldSample(trace.SamplingParameters{
			ParentContext: ctx,
			TraceID:       tid,
		})
		if result.Decision == trace.RecordAndSample {
			sampled++
		}
	}

	// Should be approximately 10% (allow some variance)
	rate := float64(sampled) / float64(total)
	assert.True(t, rate >= 0.05 && rate <= 0.15, "sampling rate should be ~10%%, got %f", rate)
}

func TestAdaptiveSampler_ParentSampling(t *testing.T) {
	sampler := NewAdaptiveSampler(WithBaselineRate(0.0)) // 0% baseline

	// Create a parent context with sampled span
	sc := oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: oteltrace.FlagsSampled,
	}
	parentCtx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(sc))

	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: parentCtx,
		TraceID:       sc.TraceID,
	})

	// Should sample because parent is sampled
	assert.Equal(t, trace.RecordAndSample, result.Decision)
}

func TestAdaptiveSampler_Options(t *testing.T) {
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.5),
		WithErrorRate(0.8),
		WithSlowThreshold(2e9), // 2 seconds
		WithSlowRate(0.9),
	)

	require.NotNil(t, sampler)
	// Just verify it doesn't panic and creates a valid sampler
	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	})
	assert.NotNil(t, result)
}
