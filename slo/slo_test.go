package slo_test

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/jagreehal/autotel-go/v2/slo"
)

func TestTrackerCalculatesSLIAndErrorBudgetBurnRate(t *testing.T) {
	now := time.Unix(1, 0)
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "checkout.availability", Target: 0.99, Window: time.Minute},
		slo.WithClock(func() time.Time { return now }),
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	for range 99 {
		_, err = tracker.Record(context.Background(), slo.OutcomeGood)
		require.NoError(t, err)
	}
	snapshot, err := tracker.Record(context.Background(), slo.OutcomeBad)
	require.NoError(t, err)

	require.Equal(t, int64(100), snapshot.Total)
	require.Equal(t, int64(99), snapshot.Good)
	require.Equal(t, int64(1), snapshot.Bad)
	require.NotNil(t, snapshot.SLI)
	require.InDelta(t, 0.99, *snapshot.SLI, 1e-12)
	require.InDelta(t, 1, snapshot.BurnRate, 1e-12)
	require.InDelta(t, 0, snapshot.BudgetRemaining, 1e-12)
	require.True(t, snapshot.MeetsTarget)

	now = now.Add(time.Millisecond)
	snapshot, err = tracker.Record(context.Background(), slo.OutcomeBad)
	require.NoError(t, err)
	require.Greater(t, snapshot.BurnRate, 1.0)
}

func TestNewTrackerRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name       string
		definition slo.Definition
	}{
		{name: "empty name", definition: slo.Definition{Target: 0.99, Window: time.Minute}},
		{name: "blank name", definition: slo.Definition{Name: "  ", Target: 0.99, Window: time.Minute}},
		{name: "zero target", definition: slo.Definition{Name: "api", Target: 0, Window: time.Minute}},
		{name: "perfect target", definition: slo.Definition{Name: "api", Target: 1, Window: time.Minute}},
		{name: "NaN target", definition: slo.Definition{Name: "api", Target: math.NaN(), Window: time.Minute}},
		{name: "zero window", definition: slo.Definition{Name: "api", Target: 0.99}},
		{name: "negative window", definition: slo.Definition{Name: "api", Target: 0.99, Window: -time.Second}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker, err := slo.NewTracker(test.definition, slo.WithMetrics(false))
			require.Error(t, err)
			require.Nil(t, tracker)
		})
	}
}

func TestTrackerDropsObservationsOutsideRollingWindow(t *testing.T) {
	now := time.Unix(0, 0)
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.9, Window: time.Second},
		slo.WithClock(func() time.Time { return now }),
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	_, err = tracker.Record(context.Background(), slo.OutcomeBad)
	require.NoError(t, err)
	now = now.Add(time.Second + time.Nanosecond)

	snapshot := tracker.Snapshot()
	require.Zero(t, snapshot.Total)
	require.Zero(t, snapshot.Good)
	require.Zero(t, snapshot.Bad)
	require.Nil(t, snapshot.SLI)
	require.Zero(t, snapshot.BurnRate)
	require.True(t, snapshot.MeetsTarget)
}

func TestTrackerResetClearsAllObservations(t *testing.T) {
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.99, Window: time.Hour},
		slo.WithMetrics(false),
	)
	require.NoError(t, err)
	_, err = tracker.Record(context.Background(), slo.OutcomeGood)
	require.NoError(t, err)
	_, err = tracker.Record(context.Background(), slo.OutcomeBad)
	require.NoError(t, err)

	tracker.Reset()

	require.Zero(t, tracker.Snapshot().Total)
}

func TestEvaluateBurnRateAlertRequiresBothWindows(t *testing.T) {
	base := slo.Snapshot{
		Definition: slo.Definition{Name: "checkout.availability", Target: 0.99},
		Total:      100,
		Good:       95,
		Bad:        5,
		BurnRate:   5,
	}

	tests := []struct {
		name       string
		shortBurn  float64
		longBurn   float64
		wantAlert  bool
		wantReason slo.BurnRateAlertReason
	}{
		{
			name:       "both thresholds exceeded",
			shortBurn:  14.5,
			longBurn:   7,
			wantAlert:  true,
			wantReason: slo.BurnRateThresholdsExceeded,
		},
		{
			name:       "short spike without sustained burn",
			shortBurn:  14.5,
			longBurn:   2,
			wantAlert:  false,
			wantReason: slo.LongWindowBelowThreshold,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shortWindow := base
			shortWindow.Window = 5 * time.Minute
			shortWindow.BurnRate = test.shortBurn
			longWindow := base
			longWindow.Window = time.Hour
			longWindow.BurnRate = test.longBurn

			decision, err := slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
				ShortWindow:    shortWindow,
				LongWindow:     longWindow,
				ShortThreshold: 14,
				LongThreshold:  6,
			})

			require.NoError(t, err)
			require.Equal(t, test.wantAlert, decision.Alerting)
			require.Equal(t, test.wantReason, decision.Reason)
			require.Equal(t, test.shortBurn, decision.ShortBurnRate)
			require.Equal(t, test.longBurn, decision.LongBurnRate)
		})
	}
}

func TestTrackerForecastsErrorBudgetExhaustion(t *testing.T) {
	now := time.Unix(0, 0)
	tracker, err := slo.NewTracker(
		slo.Definition{
			Name:   "checkout.availability",
			Target: 0.99,
			Window: 30 * 24 * time.Hour,
		},
		slo.WithClock(func() time.Time { return now }),
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	for range 9_950 {
		_, err = tracker.Record(context.Background(), slo.OutcomeGood)
		require.NoError(t, err)
	}
	now = now.Add(29 * 24 * time.Hour)
	for range 25 {
		_, err = tracker.Record(context.Background(), slo.OutcomeGood)
		require.NoError(t, err)
	}
	for range 25 {
		_, err = tracker.Record(context.Background(), slo.OutcomeBad)
		require.NoError(t, err)
	}

	expectedEvents := 1_440.0
	forecast, err := tracker.Forecast(slo.ForecastOptions{
		Baseline:                  6 * time.Hour,
		Lookahead:                 24 * time.Hour,
		ExpectedEventsInLookahead: &expectedEvents,
	})

	require.NoError(t, err)
	require.Equal(t, int64(50), forecast.BaselineTotal)
	require.Equal(t, int64(25), forecast.BaselineBad)
	require.Equal(t, int64(10_000), forecast.RetainedTotal)
	require.InDelta(t, 11_440, forecast.ProjectedTotal, 1e-12)
	require.InDelta(t, 745, forecast.ProjectedBad, 1e-12)
	require.NotNil(t, forecast.ProjectedSLI)
	require.InDelta(t, float64(11_440-745)/11_440, *forecast.ProjectedSLI, 1e-12)
	require.NotNil(t, forecast.TimeToExhaustion)
	require.Positive(t, *forecast.TimeToExhaustion)
	require.True(t, forecast.Alerting)
	require.Equal(t, slo.ProjectedBudgetExhaustion, forecast.Reason)
}

func TestForecastMatchesChapterTwelveWorkedScenarios(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	bad := make([][2]int64, 26)
	bad[0] = [2]int64{1_460, 130}
	for daysAgo := 1; daysAgo < len(bad); daysAgo++ {
		failures := int64(6)
		if daysAgo <= 5 {
			failures = 7
		}
		bad[daysAgo] = [2]int64{1_460, failures}
	}
	good := make([][2]int64, 26)
	for daysAgo := range good {
		good[daysAgo] = [2]int64{1_460, 5}
	}

	tests := []struct {
		name             string
		buckets          [][2]int64
		wantAlert        bool
		wantProjectedBad float64
	}{
		{name: "budget will be exhausted", buckets: bad, wantAlert: true, wantProjectedBad: 805},
		{name: "comfortably within budget", buckets: good, wantAlert: false, wantProjectedBad: 150},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := now.AddDate(0, 0, -(len(test.buckets) - 1))
			tracker, err := slo.NewTracker(
				slo.Definition{Name: "chapter-12", Target: 0.99, Window: 30 * 24 * time.Hour},
				slo.WithClock(func() time.Time { return clock }),
				slo.WithMetrics(false),
			)
			require.NoError(t, err)

			for daysAgo := len(test.buckets) - 1; daysAgo >= 0; daysAgo-- {
				total, failures := test.buckets[daysAgo][0], test.buckets[daysAgo][1]
				for range total - failures {
					_, err = tracker.Record(context.Background(), slo.OutcomeGood)
					require.NoError(t, err)
				}
				for range failures {
					_, err = tracker.Record(context.Background(), slo.OutcomeBad)
					require.NoError(t, err)
				}
				clock = clock.AddDate(0, 0, 1)
			}
			clock = now

			forecast, err := tracker.Forecast(slo.ForecastOptions{
				Baseline:  24 * time.Hour,
				Lookahead: 4 * 24 * time.Hour,
			})

			require.NoError(t, err)
			require.Equal(t, int64(37_960), forecast.RetainedTotal)
			require.InDelta(t, 43_800, forecast.ProjectedTotal, 1e-12)
			require.InDelta(t, test.wantProjectedBad, forecast.ProjectedBad, 1e-12)
			require.Equal(t, test.wantAlert, forecast.Alerting)
		})
	}
}

func TestTrackerRecordsLowCardinalityMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	tracker, err := slo.NewTracker(
		slo.Definition{Name: "checkout.availability", Target: 0.99, Window: time.Hour},
		slo.WithMeter(provider.Meter("slo-test")),
	)
	require.NoError(t, err)
	_, err = tracker.Record(context.Background(), slo.OutcomeBad)
	require.NoError(t, err)

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))
	var names []string
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			names = append(names, metric.Name)
		}
	}
	sort.Strings(names)
	require.Equal(t, []string{"autotel.slo.burn_rate", "autotel.slo.outcomes"}, names)
}

func TestTrackerRejectsInvalidOutcomeWithoutChangingSnapshot(t *testing.T) {
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.99, Window: time.Hour},
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	_, err = tracker.Record(context.Background(), slo.Outcome("unknown"))

	require.ErrorContains(t, err, "outcome")
	require.Zero(t, tracker.Snapshot().Total)
}

func TestForecastValidatesProjectionBounds(t *testing.T) {
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.99, Window: 30 * 24 * time.Hour},
		slo.WithMetrics(false),
	)
	require.NoError(t, err)
	negativeEvents := -1.0

	tests := []struct {
		name    string
		options slo.ForecastOptions
	}{
		{name: "baseline must be positive", options: slo.ForecastOptions{Lookahead: time.Minute}},
		{name: "lookahead must be positive", options: slo.ForecastOptions{Baseline: time.Minute}},
		{name: "lookahead limited to four baselines", options: slo.ForecastOptions{Baseline: time.Minute, Lookahead: 4*time.Minute + time.Nanosecond}},
		{name: "baseline fits SLO window", options: slo.ForecastOptions{Baseline: 31 * 24 * time.Hour, Lookahead: time.Hour}},
		{name: "expected events cannot be negative", options: slo.ForecastOptions{Baseline: time.Minute, Lookahead: time.Minute, ExpectedEventsInLookahead: &negativeEvents}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tracker.Forecast(test.options)
			require.Error(t, err)
		})
	}
}

func TestEvaluateBurnRateAlertHandlesNoTrafficAndInvalidWindows(t *testing.T) {
	base := slo.Snapshot{
		Definition: slo.Definition{Name: "checkout.availability", Target: 0.99},
		Total:      100,
		BurnRate:   10,
	}
	noTraffic := base
	noTraffic.Total = 0

	decision, err := slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
		ShortWindow:    noTraffic,
		LongWindow:     base,
		ShortThreshold: 14,
		LongThreshold:  6,
	})
	require.NoError(t, err)
	require.False(t, decision.Alerting)
	require.Equal(t, slo.BurnRateNoTraffic, decision.Reason)

	mismatch := base
	mismatch.Name = "different"
	_, err = slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
		ShortWindow:    base,
		LongWindow:     mismatch,
		ShortThreshold: 14,
		LongThreshold:  6,
	})
	require.ErrorContains(t, err, "same SLO")
}

func TestForecastWithoutBaselineTrafficStaysQuiet(t *testing.T) {
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.99, Window: time.Hour},
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	forecast, err := tracker.Forecast(slo.ForecastOptions{
		Baseline:  5 * time.Minute,
		Lookahead: 20 * time.Minute,
	})

	require.NoError(t, err)
	require.False(t, forecast.Alerting)
	require.Equal(t, slo.NoBaselineTraffic, forecast.Reason)
	require.Nil(t, forecast.ProjectedSLI)
	require.Nil(t, forecast.TimeToExhaustion)
}

func TestTrackerRecordsConcurrentOutcomes(t *testing.T) {
	tracker, err := slo.NewTracker(
		slo.Definition{Name: "api.success", Target: 0.99, Window: time.Hour},
		slo.WithMetrics(false),
	)
	require.NoError(t, err)

	errors := make(chan error, 100)
	for range 100 {
		go func() {
			_, recordErr := tracker.Record(context.Background(), slo.OutcomeGood)
			errors <- recordErr
		}()
	}
	for range 100 {
		require.NoError(t, <-errors)
	}

	require.Equal(t, int64(100), tracker.Snapshot().Total)
}
