// Package slo tracks service-level objectives and evaluates error-budget burn.
package slo

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	instrumentationScope = "github.com/jagreehal/autotel-go/v2/slo"

	// lookbackRatio is the maximum practical extrapolation factor used by the
	// predictive (context-aware) burn alert from Observability Engineering.
	lookbackRatio = 4
)

// Outcome classifies an event as satisfying or violating an SLO.
type Outcome string

const (
	OutcomeGood Outcome = "good"
	OutcomeBad  Outcome = "bad"
)

// Definition describes a rolling-window service-level objective.
type Definition struct {
	Name   string
	Target float64
	Window time.Duration
}

// Snapshot is the current state of an SLO's rolling observation window.
type Snapshot struct {
	Definition
	ObservedAt          time.Time
	Total               int64
	Good                int64
	Bad                 int64
	SLI                 *float64
	ErrorBudgetFraction float64
	BudgetConsumed      float64
	BudgetRemaining     float64
	BurnRate            float64
	MeetsTarget         bool
}

// BurnRateAlertReason explains a dual-window burn-rate decision.
type BurnRateAlertReason string

const (
	BurnRateThresholdsExceeded BurnRateAlertReason = "burn-rate-thresholds-exceeded"
	ShortWindowBelowThreshold  BurnRateAlertReason = "short-window-below-threshold"
	LongWindowBelowThreshold   BurnRateAlertReason = "long-window-below-threshold"
	BurnRateNoTraffic          BurnRateAlertReason = "no-traffic"
)

// BurnRateAlertOptions configures a dual-window burn-rate evaluation.
type BurnRateAlertOptions struct {
	ShortWindow    Snapshot
	LongWindow     Snapshot
	ShortThreshold float64
	LongThreshold  float64
}

// BurnRateAlertDecision reports whether both burn-rate windows are alerting.
type BurnRateAlertDecision struct {
	Alerting      bool
	Reason        BurnRateAlertReason
	ShortBurnRate float64
	LongBurnRate  float64
}

// ForecastReason explains a predictive error-budget decision.
type ForecastReason string

const (
	NoBaselineTraffic         ForecastReason = "no-baseline-traffic"
	WithinBudget              ForecastReason = "within-budget"
	ProjectedBudgetExhaustion ForecastReason = "projected-budget-exhaustion"
)

// ForecastOptions configures a predictive error-budget forecast.
type ForecastOptions struct {
	Baseline                  time.Duration
	Lookahead                 time.Duration
	ExpectedEventsInLookahead *float64
}

// Forecast projects recent traffic and failures into the future SLO window.
type Forecast struct {
	Name                string
	Target              float64
	ObservedAt          time.Time
	Baseline            time.Duration
	Lookahead           time.Duration
	BaselineTotal       int64
	BaselineBad         int64
	BaselineFailureRate float64
	RetainedTotal       int64
	RetainedBad         int64
	ProjectedTotal      float64
	ProjectedBad        float64
	ProjectedSLI        *float64
	TimeToExhaustion    *time.Duration
	Alerting            bool
	Reason              ForecastReason
}

// EvaluateBurnRateAlert evaluates short and long burn-rate windows together.
func EvaluateBurnRateAlert(options BurnRateAlertOptions) (BurnRateAlertDecision, error) {
	decision := BurnRateAlertDecision{
		ShortBurnRate: options.ShortWindow.BurnRate,
		LongBurnRate:  options.LongWindow.BurnRate,
	}
	if err := validateThreshold("short threshold", options.ShortThreshold); err != nil {
		return BurnRateAlertDecision{}, err
	}
	if err := validateThreshold("long threshold", options.LongThreshold); err != nil {
		return BurnRateAlertDecision{}, err
	}
	if options.ShortWindow.Name != options.LongWindow.Name || options.ShortWindow.Target != options.LongWindow.Target {
		return BurnRateAlertDecision{}, fmt.Errorf("slo: burn-rate windows must describe the same SLO name and target")
	}
	if options.ShortWindow.Total == 0 || options.LongWindow.Total == 0 {
		decision.Reason = BurnRateNoTraffic
		return decision, nil
	}
	if options.ShortWindow.BurnRate < options.ShortThreshold {
		decision.Reason = ShortWindowBelowThreshold
		return decision, nil
	}
	if options.LongWindow.BurnRate < options.LongThreshold {
		decision.Reason = LongWindowBelowThreshold
		return decision, nil
	}

	decision.Alerting = true
	decision.Reason = BurnRateThresholdsExceeded
	return decision, nil
}

func validateThreshold(name string, threshold float64) error {
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 {
		return fmt.Errorf("slo: %s must be greater than 0", name)
	}
	return nil
}

type trackerConfig struct {
	clock         func() time.Time
	recordMetrics bool
	meter         metric.Meter
}

// Option configures a Tracker.
type Option func(*trackerConfig)

// WithClock replaces the wall clock, primarily for deterministic tests.
func WithClock(clock func() time.Time) Option {
	return func(config *trackerConfig) {
		config.clock = clock
	}
}

// WithMetrics controls OpenTelemetry metric recording.
func WithMetrics(enabled bool) Option {
	return func(config *trackerConfig) {
		config.recordMetrics = enabled
	}
}

// WithMeter supplies the OpenTelemetry meter used by the tracker.
func WithMeter(meter metric.Meter) Option {
	return func(config *trackerConfig) {
		config.meter = meter
	}
}

type observation struct {
	outcome   Outcome
	timestamp time.Time
}

// Tracker records outcomes for one rolling-window SLO.
type Tracker struct {
	mu                sync.Mutex
	definition        Definition
	clock             func() time.Time
	observations      []observation
	firstLive         int
	liveGood          int64
	liveBad           int64
	outcomeCounter    metric.Int64Counter
	burnRateHistogram metric.Float64Histogram
}

// NewTracker creates a tracker for definition.
func NewTracker(definition Definition, options ...Option) (*Tracker, error) {
	if err := validateDefinition(definition); err != nil {
		return nil, err
	}

	config := trackerConfig{
		clock:         time.Now,
		recordMetrics: true,
		meter:         otel.Meter(instrumentationScope),
	}
	for _, option := range options {
		option(&config)
	}
	if config.clock == nil {
		return nil, fmt.Errorf("slo: clock must not be nil")
	}

	tracker := &Tracker{definition: definition, clock: config.clock}
	if !config.recordMetrics {
		return tracker, nil
	}
	if config.meter == nil {
		return nil, fmt.Errorf("slo: meter must not be nil")
	}

	var err error
	tracker.outcomeCounter, err = config.meter.Int64Counter(
		"autotel.slo.outcomes",
		metric.WithDescription("Good and bad events recorded against a service level objective"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("slo: create outcome counter: %w", err)
	}
	tracker.burnRateHistogram, err = config.meter.Float64Histogram(
		"autotel.slo.burn_rate",
		metric.WithDescription("Error-budget burn rate for a service level objective"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("slo: create burn-rate histogram: %w", err)
	}

	return tracker, nil
}

func validateDefinition(definition Definition) error {
	if strings.TrimSpace(definition.Name) == "" {
		return fmt.Errorf("slo: name must not be empty")
	}
	if math.IsNaN(definition.Target) || math.IsInf(definition.Target, 0) || definition.Target <= 0 || definition.Target >= 1 {
		return fmt.Errorf("slo: target must be greater than 0 and less than 1")
	}
	if definition.Window <= 0 {
		return fmt.Errorf("slo: window must be greater than 0")
	}
	return nil
}

// Record adds an outcome and returns the updated snapshot.
func (tracker *Tracker) Record(outcome Outcome, attributes ...attribute.KeyValue) (Snapshot, error) {
	if outcome != OutcomeGood && outcome != OutcomeBad {
		return Snapshot{}, fmt.Errorf("slo: outcome must be %q or %q", OutcomeGood, OutcomeBad)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	now := tracker.clock()
	tracker.observations = append(tracker.observations, observation{outcome: outcome, timestamp: now})
	if outcome == OutcomeGood {
		tracker.liveGood++
	} else {
		tracker.liveBad++
	}
	snapshot := tracker.snapshot(now)
	metricAttributes := append(
		append([]attribute.KeyValue(nil), attributes...),
		attribute.String("slo.name", tracker.definition.Name),
		attribute.String("slo.outcome", string(outcome)),
	)
	if tracker.outcomeCounter != nil {
		tracker.outcomeCounter.Add(context.Background(), 1, metric.WithAttributes(metricAttributes...))
	}
	if tracker.burnRateHistogram != nil {
		tracker.burnRateHistogram.Record(
			context.Background(),
			snapshot.BurnRate,
			metric.WithAttributes(attribute.String("slo.name", tracker.definition.Name)),
		)
	}
	return snapshot, nil
}

// Snapshot returns the current rolling-window state.
func (tracker *Tracker) Snapshot() Snapshot {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	return tracker.snapshot(tracker.clock())
}

// Forecast projects the recent baseline through the requested lookahead.
func (tracker *Tracker) Forecast(options ForecastOptions) (Forecast, error) {
	if err := validateForecastOptions(options, tracker.definition.Window); err != nil {
		return Forecast{}, err
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	observedAt := tracker.clock()
	tracker.prune(observedAt)
	baselineCutoff := observedAt.Add(-options.Baseline)
	retainedCutoff := observedAt.Add(options.Lookahead - tracker.definition.Window)

	var baselineTotal, baselineBad, retainedTotal, retainedBad int64
	for index := tracker.firstLive; index < len(tracker.observations); index++ {
		observation := tracker.observations[index]
		if observation.timestamp.After(baselineCutoff) {
			baselineTotal++
			if observation.outcome == OutcomeBad {
				baselineBad++
			}
		}
		if !observation.timestamp.Before(retainedCutoff) {
			retainedTotal++
			if observation.outcome == OutcomeBad {
				retainedBad++
			}
		}
	}

	forecast := Forecast{
		Name:          tracker.definition.Name,
		Target:        tracker.definition.Target,
		ObservedAt:    observedAt,
		Baseline:      options.Baseline,
		Lookahead:     options.Lookahead,
		BaselineTotal: baselineTotal,
		BaselineBad:   baselineBad,
		RetainedTotal: retainedTotal,
		RetainedBad:   retainedBad,
	}
	if baselineTotal == 0 {
		forecast.ProjectedTotal = float64(retainedTotal)
		forecast.ProjectedBad = float64(retainedBad)
		forecast.ProjectedSLI = calculateSLI(forecast.ProjectedTotal, forecast.ProjectedBad)
		forecast.Reason = NoBaselineTraffic
		return forecast, nil
	}

	forecast.BaselineFailureRate = float64(baselineBad) / float64(baselineTotal)
	expectedEvents := float64(baselineTotal) * float64(options.Lookahead) / float64(options.Baseline)
	if options.ExpectedEventsInLookahead != nil {
		expectedEvents = *options.ExpectedEventsInLookahead
	}
	projectedFailures := forecast.BaselineFailureRate * expectedEvents
	forecast.ProjectedTotal = float64(retainedTotal) + expectedEvents
	forecast.ProjectedBad = float64(retainedBad) + projectedFailures
	forecast.ProjectedSLI = calculateSLI(forecast.ProjectedTotal, forecast.ProjectedBad)

	allowedFailureRate := 1 - tracker.definition.Target
	remainingFailures := allowedFailureRate*float64(retainedTotal) - float64(retainedBad)
	netFailureRate := forecast.BaselineFailureRate - allowedFailureRate
	eventRatePerSecond := expectedEvents / options.Lookahead.Seconds()
	switch {
	case remainingFailures <= 0:
		duration := time.Duration(0)
		forecast.TimeToExhaustion = &duration
	case netFailureRate > 0 && eventRatePerSecond > 0:
		seconds := remainingFailures / (netFailureRate * eventRatePerSecond)
		duration := time.Duration(seconds * float64(time.Second))
		forecast.TimeToExhaustion = &duration
	}

	forecast.Alerting = forecast.ProjectedSLI != nil && *forecast.ProjectedSLI < tracker.definition.Target
	if forecast.Alerting {
		forecast.Reason = ProjectedBudgetExhaustion
	} else {
		forecast.Reason = WithinBudget
	}
	return forecast, nil
}

func validateForecastOptions(options ForecastOptions, window time.Duration) error {
	if options.Baseline <= 0 {
		return fmt.Errorf("slo: baseline must be greater than 0")
	}
	if options.Lookahead <= 0 {
		return fmt.Errorf("slo: lookahead must be greater than 0")
	}
	if float64(options.Lookahead) > lookbackRatio*float64(options.Baseline) {
		return fmt.Errorf("slo: lookahead must not exceed %d times baseline", lookbackRatio)
	}
	if options.Baseline > window {
		return fmt.Errorf("slo: baseline must not exceed the SLO window")
	}
	if expected := options.ExpectedEventsInLookahead; expected != nil &&
		(math.IsNaN(*expected) || math.IsInf(*expected, 0) || *expected < 0) {
		return fmt.Errorf("slo: expected events in lookahead must not be negative")
	}
	return nil
}

func calculateSLI(total, bad float64) *float64 {
	if total == 0 {
		return nil
	}
	value := (total - bad) / total
	return &value
}

// Reset removes every recorded observation.
func (tracker *Tracker) Reset() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	tracker.observations = nil
	tracker.firstLive = 0
	tracker.liveGood = 0
	tracker.liveBad = 0
}

func (tracker *Tracker) snapshot(at time.Time) Snapshot {
	tracker.prune(at)

	good := tracker.liveGood
	bad := tracker.liveBad
	total := good + bad
	errorBudgetFraction := 1 - tracker.definition.Target
	observedBadFraction := float64(0)
	var sli *float64
	if total > 0 {
		value := float64(good) / float64(total)
		sli = &value
		observedBadFraction = float64(bad) / float64(total)
	}
	burnRate := observedBadFraction / errorBudgetFraction

	return Snapshot{
		Definition:          tracker.definition,
		ObservedAt:          at,
		Total:               total,
		Good:                good,
		Bad:                 bad,
		SLI:                 sli,
		ErrorBudgetFraction: errorBudgetFraction,
		BudgetConsumed:      burnRate,
		BudgetRemaining:     1 - burnRate,
		BurnRate:            burnRate,
		MeetsTarget:         sli == nil || *sli >= tracker.definition.Target,
	}
}

func (tracker *Tracker) prune(at time.Time) {
	cutoff := at.Add(-tracker.definition.Window)
	for tracker.firstLive < len(tracker.observations) && tracker.observations[tracker.firstLive].timestamp.Before(cutoff) {
		if tracker.observations[tracker.firstLive].outcome == OutcomeGood {
			tracker.liveGood--
		} else {
			tracker.liveBad--
		}
		tracker.firstLive++
	}

	if tracker.firstLive > 1024 && tracker.firstLive*2 >= len(tracker.observations) {
		tracker.observations = append([]observation(nil), tracker.observations[tracker.firstLive:]...)
		tracker.firstLive = 0
	}
}
