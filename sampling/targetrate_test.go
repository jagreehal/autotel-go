package sampling

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// drive sends n spans through the sampler and reports how many were kept. Trace
// IDs vary so the deterministic keep decision spreads across the range.
func drive(s *TargetRateSampler, n int, attrs ...attribute.KeyValue) int {
	kept := 0
	for i := 0; i < n; i++ {
		var traceID oteltrace.TraceID
		traceID[15] = byte(i % 256)
		traceID[0] = byte(i / 256)
		result := s.ShouldSample(sdktrace.SamplingParameters{
			ParentContext: context.Background(),
			TraceID:       traceID,
			Name:          "request",
			Attributes:    attrs,
		})
		if result.Decision == sdktrace.RecordAndSample {
			kept++
		}
	}
	return kept
}

// The whole point: the same configuration keeps roughly the same number of
// traces per second whether traffic is light or heavy. A fixed rate cannot.
func TestTargetRateHoldsTheBudgetAcrossTrafficLevels(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	sampler := NewTargetRateSampler(
		WithTargetSpansPerSecond(10),
		WithAdjustInterval(time.Minute),
		WithTargetRateClock(clock.Now),
	)

	// First window has no measurement behind it, so everything is kept.
	if kept := drive(sampler, 6000); kept != 6000 {
		t.Fatalf("first window kept %d/6000; with nothing measured it must keep all", kept)
	}

	// 6000 spans a minute is 100/s against a budget of 10/s, so the next window
	// should keep about a tenth.
	clock.advance(time.Minute)
	kept := drive(sampler, 6000)
	if kept < 400 || kept > 800 {
		t.Errorf("kept %d/6000 at 100/s against a 10/s budget, want ~600", kept)
	}

	// Traffic collapses to a tenth. The rate in force was computed from the
	// previous window, so this one is still throttled: the sampler cannot know
	// traffic fell until it has finished measuring it.
	clock.advance(time.Minute)
	if kept := drive(sampler, 600); kept > 200 {
		t.Errorf("kept %d/600 in the window where the drop happened, want the old rate to still apply", kept)
	}

	// Having now measured 10/s, exactly the budget, it stops throttling.
	clock.advance(time.Minute)
	if kept := drive(sampler, 600); kept != 600 {
		t.Errorf("kept %d/600 once the lower volume had been measured, want all of it", kept)
	}
}

// A noisy key must not spend the budget belonging to a quiet one.
func TestTargetRateBudgetsPerKey(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	sampler := NewTargetRateSampler(
		WithTargetSpansPerSecond(10),
		WithAdjustInterval(time.Minute),
		WithTargetRateClock(clock.Now),
		WithSamplingKey(KeyByAttributes("http.route")),
	)

	noisy := attribute.String("http.route", "/health")
	quiet := attribute.String("http.route", "/checkout")

	drive(sampler, 6000, noisy)
	drive(sampler, 300, quiet)

	clock.advance(time.Minute)

	keptNoisy := drive(sampler, 6000, noisy)
	keptQuiet := drive(sampler, 300, quiet)

	if keptNoisy > 1000 {
		t.Errorf("the noisy route kept %d/6000; its budget should have bound it", keptNoisy)
	}
	if keptQuiet != 300 {
		t.Errorf("the quiet route kept %d/300; it is under budget and should survive intact", keptQuiet)
	}
}

// An unbounded key must degrade into inaccuracy, not into memory exhaustion.
func TestTargetRateBoundsTrackedKeys(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	sampler := NewTargetRateSampler(
		WithAdjustInterval(time.Minute),
		WithTargetRateClock(clock.Now),
		WithMaxSamplingKeys(50),
		WithSamplingKey(KeyByAttributes("request.id")),
	)

	for i := 0; i < 500; i++ {
		drive(sampler, 1, attribute.Int("request.id", i))
	}
	clock.advance(time.Minute)
	drive(sampler, 1, attribute.Int("request.id", 9999))

	sampler.mu.Lock()
	tracked := len(sampler.rates)
	sampler.mu.Unlock()

	if tracked > 50 {
		t.Errorf("tracking %d keys, want at most the configured 50", tracked)
	}
}

// A sampled parent must carry its children, or the budget buys broken traces.
func TestTargetRateFollowsASampledParent(t *testing.T) {
	sampler := NewTargetRateSampler(WithTargetSpansPerSecond(0.0001))

	parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1},
		SpanID:     oteltrace.SpanID{1},
		TraceFlags: oteltrace.FlagsSampled,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), parent)

	result := sampler.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: ctx,
		TraceID:       oteltrace.TraceID{1},
		Name:          "child",
	})
	if result.Decision != sdktrace.RecordAndSample {
		t.Error("a child of a sampled parent was dropped")
	}
}
