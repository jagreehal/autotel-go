// Observability Engineering, chapter 15: cheap and accurate enough sampling.
//
// The chapter is a ladder. Each rung fixes what the one below it broke, and the
// authors implement all nine in Go in their companion repository under
// 2e/chapter-15-sampling. Read them alongside this.
//
// This walks the same ladder using autotel-go, in the same order, and asserts
// each rung against real exported spans rather than describing it. Where the
// library does not climb a rung, it says so instead of quietly skipping it.
//
// No backend and no API key: every rung exports into memory.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

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
	rung6TwoRates()
	rung9HeadAndTail()
	unclimbed()
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

// --- The rungs this library does not climb. ---------------------------------

func unclimbed() {
	fmt.Println()
	fmt.Println("  Not covered, and worth saying plainly:")
	fmt.Println("    5 target rate        needs a feedback loop that recomputes the rate")
	fmt.Println("                         from observed volume; autotel's rates are static")
	fmt.Println("    7 key + target rate  same, per traffic class")
	fmt.Println("    8 dynamic many keys  same, with per-key rates recomputed in background")
	fmt.Println()
	fmt.Println("  Rungs 5, 7 and 8 all want the same missing piece: a rate that")
	fmt.Println("  adapts to traffic. Until that exists, budget by hand.")
}

// --- harness ----------------------------------------------------------------

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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
