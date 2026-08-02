// Observability Engineering, chapter 12: acting on and debugging SLO-based alerts.
//
// The chapter's argument is that a fixed error-rate threshold is the wrong
// trigger. What matters is whether the error budget will actually run out, so
// the alert projects the recent baseline forward and fires only if exhaustion
// lands inside the window you care about.
//
// The authors implement that projection in Go in their companion repository
// (2e/chapter-12-slo-based-alerts/burnalert.go, 86 lines plus a 99-line test).
// Their version is the calculation on its own: you supply the bucketed
// timeseries, and isBurnViolation returns a bool.
//
// This runs the same three ideas against autotel-go's slo package — budget burn,
// two windows that must agree, and a forward projection — recording outcomes as
// they happen rather than assembling a timeseries by hand. Every number printed
// below is asserted first.
//
// The clock is injected, so the six-day result is exact rather than a test that
// waits six days.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jagreehal/autotel-go/v2/slo"
)

func main() {
	fmt.Println("OE 12: alerting on budget burn, not on a threshold")

	budgetBurn()
	twoWindows()
	projection()
}

// --- The budget, and how fast five failures per thousand spend it. ----------

// Chapter 11 sets up what chapter 12 alerts on: a 99.9% monthly objective
// permits 1 failure in 1,000. Serve five and the month's budget is gone in days.
func budgetBurn() {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tracker, err := slo.NewTracker(slo.Definition{
		Name:   "checkout-availability",
		Target: 0.999,
		Window: 30 * 24 * time.Hour,
	}, slo.WithClock(clock.Now), slo.WithMetrics(false))
	if err != nil {
		fail("tracker: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		outcome := slo.OutcomeGood
		if i%200 == 0 { // five per thousand
			outcome = slo.OutcomeBad
		}
		if _, err := tracker.Record(ctx, outcome); err != nil {
			fail("record: %v", err)
		}
	}

	s := tracker.Snapshot()
	if s.Bad != 5 {
		fail("expected 5 failures, got %d", s.Bad)
	}
	// 0.5% observed against a 0.1% budget: five times the sustainable rate.
	if s.BurnRate < 4.9 || s.BurnRate > 5.1 {
		fail("burn rate = %.2f, want ~5", s.BurnRate)
	}
	if s.MeetsTarget {
		fail("0.5%% failures cannot meet a 99.9%% target")
	}

	days := 30 / s.BurnRate
	fmt.Printf("  budget       %d/%d failed, burn rate %.1fx\n", s.Bad, s.Total, s.BurnRate)
	fmt.Printf("               a 30-day budget gone in %.0f days\n", days)
}

// --- Two windows, so a brief spike does not page anyone. --------------------

func twoWindows() {
	// A short window alone fires on any blip. The long window is what
	// distinguishes a spike that passed from a burn that is still going.
	spike, err := slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
		ShortWindow:    window(14.4), // burning fast right now
		LongWindow:     window(0.5),  // but the hour looks fine
		ShortThreshold: 14.0,
		LongThreshold:  6.0,
	})
	if err != nil {
		fail("spike: %v", err)
	}
	if spike.Alerting {
		fail("a spike the long window disagrees with should not page")
	}
	if spike.Reason != slo.LongWindowBelowThreshold {
		fail("spike reason = %q", spike.Reason)
	}

	sustained, err := slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
		ShortWindow:    window(14.4),
		LongWindow:     window(7.0), // and it is still going
		ShortThreshold: 14.0,
		LongThreshold:  6.0,
	})
	if err != nil {
		fail("sustained: %v", err)
	}
	if !sustained.Alerting {
		fail("both windows over threshold must page")
	}

	fmt.Printf("  two windows  spike: %s (silent)\n", spike.Reason)
	fmt.Printf("               sustained: %s (pages)\n", sustained.Reason)
}

// --- The projection the chapter's own Go code computes. ---------------------

func projection() {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tracker, err := slo.NewTracker(slo.Definition{
		Name:   "checkout-availability",
		Target: 0.999,
		Window: 30 * 24 * time.Hour,
	}, slo.WithClock(clock.Now), slo.WithMetrics(false))
	if err != nil {
		fail("tracker: %v", err)
	}

	// The situation the chapter describes: a month of healthy traffic, then a
	// deploy. Most of the budget is still there, so nothing is on fire yet and a
	// threshold alert stays quiet. What matters is the direction of travel.
	ctx := context.Background()
	for i := 0; i < 20000; i++ {
		if _, err := tracker.Record(ctx, slo.OutcomeGood); err != nil {
			fail("record: %v", err)
		}
		clock.advance(128 * time.Second) // ~29.6 days of clean history
	}

	// Six hours since the deploy, failing at 0.5%: five times sustainable, but
	// still a small dent in a month's budget.
	for i := 0; i < 2000; i++ {
		outcome := slo.OutcomeGood
		if i%200 == 0 {
			outcome = slo.OutcomeBad
		}
		if _, err := tracker.Record(ctx, outcome); err != nil {
			fail("record: %v", err)
		}
		clock.advance(11 * time.Second)
	}

	// The chapter caps extrapolation at four times the baseline, and the library
	// enforces it: a 24-hour projection has to stand on six hours of evidence.
	// The book encodes the same limit as lookbackRatio = 4.
	forecast, err := tracker.Forecast(slo.ForecastOptions{
		Baseline:  6 * time.Hour,
		Lookahead: 24 * time.Hour,
	})
	if err != nil {
		fail("forecast: %v", err)
	}

	if !forecast.Alerting {
		fail("a 50x burn rate must project exhaustion inside 24h, got %q", forecast.Reason)
	}
	if forecast.Reason != slo.ProjectedBudgetExhaustion {
		fail("forecast reason = %q", forecast.Reason)
	}
	if forecast.TimeToExhaustion == nil {
		fail("an alerting forecast must say when the budget runs out")
	}
	// The point of the exercise: exhaustion is ahead of us, not behind. A zero
	// here would mean the budget was already spent, which needs no forecast.
	if *forecast.TimeToExhaustion <= 0 {
		fail("the budget was already exhausted; nothing was predicted")
	}
	if *forecast.TimeToExhaustion > 24*time.Hour {
		fail("exhaustion at %s is outside the window; this should not page",
			*forecast.TimeToExhaustion)
	}

	fmt.Printf("  projection   baseline %.1f%% failures over %s\n",
		forecast.BaselineFailureRate*100, forecast.Baseline)
	fmt.Printf("               budget exhausted in %s, inside the %s window\n",
		forecast.TimeToExhaustion.Round(time.Minute), forecast.Lookahead)
	fmt.Printf("               reason: %s\n", forecast.Reason)
}

// --- harness ----------------------------------------------------------------

func window(burnRate float64) slo.Snapshot {
	return slo.Snapshot{
		Definition: slo.Definition{Name: "checkout-availability", Target: 0.999},
		Total:      10000,
		BurnRate:   burnRate,
	}
}

// fakeClock lets the six-day and 24-hour results be exact instead of waited for.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
