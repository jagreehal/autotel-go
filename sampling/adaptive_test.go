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

func TestAdaptiveSampler_LinksBasedSampling(t *testing.T) {
	// Create sampler with 0% baseline but links-based enabled at 100%
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0), // No baseline sampling
		WithLinksBased(true),
		WithLinksRate(1.0), // 100% when linked to sampled span
	)

	// Create a sampled span context for the link
	sampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: oteltrace.FlagsSampled,
	})

	link := oteltrace.Link{SpanContext: sampledSC}

	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       oteltrace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
		Links:         []oteltrace.Link{link},
	})

	// Should sample because the link is to a sampled span
	assert.Equal(t, trace.RecordAndSample, result.Decision)
}

func TestAdaptiveSampler_LinksBasedSampling_Disabled(t *testing.T) {
	// Create sampler with links-based disabled (default)
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0), // No baseline sampling
		// linksBased defaults to false
	)

	// Create a sampled span context for the link
	sampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: oteltrace.FlagsSampled,
	})

	link := oteltrace.Link{SpanContext: sampledSC}

	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       oteltrace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
		Links:         []oteltrace.Link{link},
	})

	// Should NOT sample because links-based is disabled
	assert.Equal(t, trace.Drop, result.Decision)
}

func TestAdaptiveSampler_LinksBasedSampling_UnsampledLink(t *testing.T) {
	// Create sampler with links-based enabled
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0), // No baseline sampling
		WithLinksBased(true),
		WithLinksRate(1.0),
	)

	// Create an UNSAMPLED span context for the link (no FlagsSampled)
	unsampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: 0, // Not sampled
	})

	link := oteltrace.Link{SpanContext: unsampledSC}

	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       oteltrace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
		Links:         []oteltrace.Link{link},
	})

	// Should NOT sample because the link is to an unsampled span
	assert.Equal(t, trace.Drop, result.Decision)
}

func TestAdaptiveSampler_LinksBasedSampling_MixedLinks(t *testing.T) {
	// Create sampler with links-based enabled
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0),
		WithLinksBased(true),
		WithLinksRate(1.0),
	)

	// Create one unsampled and one sampled link
	unsampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: 0, // Not sampled
	})
	sampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		SpanID:     oteltrace.SpanID{2, 2, 2, 2, 2, 2, 2, 2},
		TraceFlags: oteltrace.FlagsSampled,
	})

	links := []oteltrace.Link{
		{SpanContext: unsampledSC},
		{SpanContext: sampledSC},
	}

	result := sampler.ShouldSample(trace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       oteltrace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
		Links:         links,
	})

	// Should sample because at least one link is sampled
	assert.Equal(t, trace.RecordAndSample, result.Decision)
}

func TestAdaptiveSampler_LinksBasedSampling_Rate(t *testing.T) {
	// Create sampler with links-based at 50% rate
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.0),
		WithLinksBased(true),
		WithLinksRate(0.5), // 50%
	)

	sampledSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: oteltrace.FlagsSampled,
	})
	link := oteltrace.Link{SpanContext: sampledSC}

	// Test with many trace IDs to verify rate
	sampled := 0
	total := 1000
	for i := 0; i < total; i++ {
		tid := oteltrace.TraceID{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i >> 32),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i >> 32),
			byte(i >> 32), byte(i >> 32), byte(i >> 32), byte(i)}
		result := sampler.ShouldSample(trace.SamplingParameters{
			ParentContext: context.Background(),
			TraceID:       tid,
			Links:         []oteltrace.Link{link},
		})
		if result.Decision == trace.RecordAndSample {
			sampled++
		}
	}

	// Should be approximately 50% (allow some variance)
	rate := float64(sampled) / float64(total)
	assert.True(t, rate >= 0.4 && rate <= 0.6, "sampling rate should be ~50%%, got %f", rate)
}
