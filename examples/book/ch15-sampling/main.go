// Observability Engineering, chapter 15: cheap and accurate enough sampling.
//
// The chapter is a ladder. Each rung fixes what the one below it broke, and the
// authors implement all nine in Go in their companion repository under
// 2e/chapter-15-sampling. Read them alongside this.
//
// This walks all nine using autotel-go, in the same order, and asserts each rung
// rather than describing it.
//
// Rungs 5, 7 and 8 were not possible when this file was first written: they need
// a rate that adapts to observed volume, and the library only had static ones.
// Writing the example is what made that gap concrete enough to close.
//
// No backend and no API key: every rung exports into memory, and the target-rate
// rungs inject a clock so a minute passes instantly.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/backends"
	"github.com/jagreehal/autotel-go/v2/sampling"
	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

const traces = 400

func main() {
	fmt.Println("OE 15: the sampling ladder")

	rung1KeepEverything()
	rung2FixedRate()
	rung3RecordTheRate()
	rung4Consistent()
	rung5TargetRate()
	rung6TwoRates()
	rung78PerKeyTargetRate()
	rung9HeadAndTail()
}

// --- 1. Keep everything. Correct, and the bill proves it. --------------------

func rung1KeepEverything() {
	kept := run(traces, sampler(1.0))
	if kept != traces {
		fail("rung 1 kept %d/%d; a baseline of 1.0 must keep everything", kept, traces)
	}
	fmt.Printf("  1 keep everything    %d/%d, and you pay for %d\n", kept, traces, traces)
}

// --- 2. A fixed rate. Cheap, and now every count you run is wrong. -----------

func rung2FixedRate() {
	kept := run(traces, sampler(0.25))
	// The decision is deterministic per trace, but trace IDs are random, so the
	// count is still binomial: mean 100, sd 8.7. This band is five sd wide on
	// each side, because an example that flakes in CI teaches nobody anything.
	if kept < 57 || kept > 143 {
		fail("rung 2 kept %d/%d at a 25%% rate", kept, traces)
	}
	fmt.Printf("  2 fixed rate         %d/%d kept, and %d is now unknowable\n", kept, traces, traces)
}

// --- 3. Record the rate, and the count becomes recoverable again. ------------

func rung3RecordTheRate() {
	cfg := autotel.DefaultConfig()
	backends.Honeycomb(backends.HoneycombConfig{
		APIKey: "hcaik_example", Service: "ladder", SampleRate: 10,
	})(cfg)

	if got := cfg.ResourceAttributes["SampleRate"]; got != "10" {
		fail("rung 3: sample rate reached the backend as %q, want \"10\"", got)
	}
	fmt.Printf("  3 record the rate    SampleRate=%s travels with the spans\n", cfg.ResourceAttributes["SampleRate"])
}

// --- 4. Consistency. The book propagates a Sampling-ID header to get this. ---

func rung4Consistent() {
	exporter := autoteltesting.NewInMemoryExporter()
	cleanup := start(exporter, sampler(0.25))

	// Each trace is a parent with a child. A trace must arrive whole or not at
	// all; a waterfall missing its middle is the failure this rung exists to fix.
	for i := 0; i < traces; i++ {
		ctx, parent := autotel.Start(context.Background(), "parent")
		_, child := autotel.Start(ctx, "child")
		child.End()
		parent.End()
	}
	cleanup()

	perTrace := map[string]int{}
	for _, s := range exporter.GetSpans() {
		perTrace[s.SpanContext().TraceID().String()]++
	}
	for id, n := range perTrace {
		if n != 2 {
			fail("rung 4: trace %s arrived with %d of its 2 spans", id, n)
		}
	}
	fmt.Printf("  4 consistent         %d whole traces, no half-sampled waterfalls\n", len(perTrace))
}

// --- 5. A budget, so nobody re-tunes a rate when traffic moves. --------------

func rung5TargetRate() {
	// The book recomputes the rate once a minute from observed volume. The clock
	// is injected here so a minute passes instantly.
	clock := &fakeClock{now: time.Unix(0, 0)}
	sampler := sampling.NewTargetRateSampler(
		sampling.WithTargetSpansPerSecond(10),
		sampling.WithAdjustInterval(time.Minute),
		sampling.WithTargetRateClock(clock.Now),
	)

	// A minute of heavy traffic teaches it the volume: 6,000 a minute is 100/s.
	feed(sampler, 6000)
	clock.advance(time.Minute)

	heavy := feed(sampler, 6000)
	// Traffic then collapses. The budget is no longer binding, and after one
	// interval of measuring the lower volume the sampler stops throwing work away.
	clock.advance(time.Minute)
	feed(sampler, 600)
	clock.advance(time.Minute)
	light := feed(sampler, 600)

	if heavy > 1000 {
		fail("rung 5 kept %d/6000 under load; a 10/s budget should bind", heavy)
	}
	if light != 600 {
		fail("rung 5 kept %d/600 on light traffic; the budget should not bind", light)
	}
	fmt.Printf("  5 target rate        %d/6000 under load, %d/600 when quiet\n", heavy, light)
	fmt.Println("                       same config, no rate re-tuned by hand")
}

// --- 6. Two rates: routine traffic cheap, outliers kept. ---------------------

func rung6TwoRates() {
	exporter := autoteltesting.NewInMemoryExporter()
	cleanup := start(exporter, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0), // keep no routine traffic at all
		sampling.WithErrorRate(1.0),  // keep every failure
	))

	errors := 0
	for i := 0; i < traces; i++ {
		_, span := autotel.Start(context.Background(), "request")
		if i%40 == 0 {
			span.SetStatus(codes.Error, "payment declined")
			errors++
		}
		span.End()
	}
	cleanup()

	kept := len(exporter.GetSpans())
	if kept != errors {
		fail("rung 6 kept %d spans; want exactly the %d errors", kept, errors)
	}
	fmt.Printf("  6 two rates          %d/%d kept: every error, no routine traffic\n", kept, traces)
}

// --- 7 & 8. A budget per key, so one loud endpoint cannot spend it all. ------

func rung78PerKeyTargetRate() {
	clock := &fakeClock{now: time.Unix(0, 0)}
	sampler := sampling.NewTargetRateSampler(
		sampling.WithTargetSpansPerSecond(10),
		sampling.WithAdjustInterval(time.Minute),
		sampling.WithTargetRateClock(clock.Now),
		// Rung 8's point: keys are discovered from traffic, not enumerated in a
		// switch statement written in advance.
		sampling.WithSamplingKey(sampling.KeyByAttributes("http.route")),
	)

	health := attribute.String("http.route", "/health")
	checkout := attribute.String("http.route", "/checkout")

	feed(sampler, 6000, health)
	feed(sampler, 300, checkout)
	clock.advance(time.Minute)

	keptHealth := feed(sampler, 6000, health)
	keptCheckout := feed(sampler, 300, checkout)

	if keptHealth > 1000 {
		fail("rung 7/8: /health kept %d/6000; its own budget should bind it", keptHealth)
	}
	if keptCheckout != 300 {
		fail("rung 7/8: /checkout kept %d/300; a quiet route should survive intact", keptCheckout)
	}
	fmt.Printf("  7 key + target rate  /health %d/6000, /checkout %d/300\n", keptHealth, keptCheckout)
	fmt.Println("  8 dynamic many keys  the noisy route cannot spend the quiet one's budget")
}

// --- 9. Head and tail. The rung above is this mechanism; here it is directly. -

func rung9HeadAndTail() {
	exporter := autoteltesting.NewInMemoryExporter()
	cleanup := start(exporter, autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0),
		sampling.WithErrorRate(0),
		sampling.WithSlowThreshold(int64(20*time.Millisecond)),
		sampling.WithSlowRate(1.0),
	))

	_, quick := autotel.Start(context.Background(), "quick")
	quick.End()

	_, slow := autotel.Start(context.Background(), "slow")
	time.Sleep(30 * time.Millisecond)
	slow.End()
	cleanup()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name() != "slow" {
		fail("rung 9 kept %v; want only the slow span", names(spans))
	}
	fmt.Println("  9 head and tail      the slow span survives a zero baseline")
	fmt.Println("                       (duration only exists once a span ends,")
	fmt.Println("                        so this decision cannot be a head one)")
}

// --- harness ----------------------------------------------------------------

// feed sends n spans through a sampler directly and reports how many it kept.
// The target-rate rungs are measured here rather than through a provider so the
// injected clock can move a minute at a time.
func feed(sampler *sampling.TargetRateSampler, n int, attrs ...attribute.KeyValue) int {
	kept := 0
	for i := 0; i < n; i++ {
		var traceID oteltrace.TraceID
		traceID[15] = byte(i % 256)
		traceID[0] = byte(i / 256)
		result := sampler.ShouldSample(sdktrace.SamplingParameters{
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

// run exports n single-span traces under opts and reports how many survived.
func run(n int, opts ...autotel.Option) int {
	exporter := autoteltesting.NewInMemoryExporter()
	cleanup := start(exporter, opts...)
	for i := 0; i < n; i++ {
		_, span := autotel.Start(context.Background(), "request")
		span.End()
	}
	cleanup()
	return len(exporter.GetSpans())
}

func sampler(baseline float64) autotel.Option {
	// Error and slow rates at zero keep this rung purely head-based, so the
	// baseline is the only thing under test.
	return autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(baseline),
		sampling.WithErrorRate(0),
		sampling.WithSlowRate(0),
	)
}

func start(exporter *autoteltesting.InMemoryExporter, opts ...autotel.Option) func() {
	base := []autotel.Option{
		autotel.WithService("ladder"),
		// Debug mode replaces the sampler with AlwaysSample, which would make
		// every assertion below pass for the wrong reason.
		autotel.WithDebug(false),
		autotel.WithMetrics(false),
		autotel.WithSpanExporters(exporter),
		autotel.WithBatchTimeout(10 * time.Millisecond),
	}
	cleanup, err := autotel.Init(context.Background(), append(base, opts...)...)
	if err != nil {
		fail("init: %v", err)
	}
	return cleanup
}

func names(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

// fakeClock lets a minute pass instantly, so the target-rate rungs do not have
// to wait for one.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
