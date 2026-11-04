# Advanced features

Use these once your service has traces: track reliability targets, record one rich event per request, write errors people can act on, find what a set of slow requests has in common, and send product events.

## Track an SLO and its burn rate

A *service-level objective* (SLO) is a reliability target, such as 99% of checkouts succeeding over 30 days. The 1% you may fail is your *error budget*, and the *burn rate* says how fast you're spending it: 1.0 uses the budget up exactly at the end of the window, and 5.0 uses it up five times sooner.

```go
import "github.com/jagreehal/autotel-go/v2/slo"

tracker, err := slo.NewTracker(slo.Definition{
	Name:   "checkout.availability",
	Target: 0.99,
	Window: 30 * 24 * time.Hour,
})
if err != nil {
	log.Fatal(err)
}

snapshot, err := tracker.Record(ctx, slo.OutcomeGood) // or slo.OutcomeBad
if err != nil {
	log.Fatal(err)
}

forecast, err := tracker.Forecast(slo.ForecastOptions{
	Baseline:  6 * time.Hour,
	Lookahead: 24 * time.Hour, // at most four times the baseline
})
if err != nil {
	log.Fatal(err)
}

fmt.Printf("burn: %.2fx, forecast alerting: %t\n", snapshot.BurnRate, forecast.Alerting)
```

`Forecast` projects recent failures forward and says whether they'd exhaust the budget. To page only when a short and a long window agree, pass a `Snapshot` of each to `slo.EvaluateBurnRateAlert`. A spike that has passed then stays quiet. [`ch12-burn-alerts`](../examples/book/ch12-burn-alerts) shows both.

The tracker emits the `autotel.slo.outcomes` and `autotel.slo.burn_rate` metrics. Use `slo.WithMeter` to send them to a specific meter provider, `slo.WithMetrics(false)` to calculate only, and `slo.WithClock` for tests and simulations.

## Record one wide event per request

A *wide event* is a single record holding everything a request learned, so you can answer questions you didn't plan for. `NewRequestLogger` builds it on the span in `ctx`:

```go
event := autotel.NewRequestLogger(ctx)
defer event.EmitNow() // one record, on success or failure

event.Set(map[string]any{
	"user":  map[string]any{"id": user.ID, "plan": user.Plan}, // user.id, user.plan
	"order": map[string]any{"total_pence": total},
})
```

Nested maps flatten to dotted attributes. Each `Set` reaches the span at once, so a request that fails halfway still carries what it knew.

## Write errors people can act on

`StructuredError` carries what a user, a support agent and an engineer each need:

```go
return &autotel.StructuredError{
	Message: "Daily limit reached",
	Why:     "This amount would take today's total past the limit.",
	Fix:     "The limit resets at midnight.",
	Code:    "DAILY_LIMIT_EXCEEDED",
	Status:  429,
	Cause:   err,
}
```

A span that records it gains `error.why`, `error.fix`, `error.link`, `error.code`, `error.status` and `error.details.*`. `autotel.Trace` records any error your function returns.

Where you catch the error, `autotel.ParseError(err)` gives you `Message`, `Why`, `Fix`, `Link`, `Code` and `Status` (500 if unset). `MarshalJSON` leaves out `Internal`, so backend-only context can't reach a client through an error response.

## Find the cohort behind a regression

A *cohort* is a group of events that share a value. Give `analysis.CompareCohorts` your slow requests and some normal ones, and it ranks the fields that set them apart:

```go
import "github.com/jagreehal/autotel-go/v2/analysis"

differences := analysis.CompareCohorts(analysis.Options{Outlier: slow, Baseline: normal})
// differences[0]: {Field: "account.shard", Value: "legacy-core", OutlierFraction: 1, BaselineFraction: 0}

band := analysis.Bucket(float64(amount), []float64{1000, 2000, 5000}) // "1000-2000"
```

It skips fields whose values rarely repeat, so turn numbers into ranges with `Bucket` when you record them.

## Add trace IDs to your own logs

`autotel.Logger` sends logs to your backend. To keep logging to stdout with trace IDs attached, wrap your `slog` handler:

```go
import "github.com/jagreehal/autotel-go/v2/logging"

logger := slog.New(logging.NewTraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
logger.InfoContext(ctx, "processing request") // adds trace_id and span_id
```

To add them to one line yourself, use `logging.WithTraceContext(ctx)` with `slog`, or `logging.TraceFields(ctx)` with zap.

## Send product events

Product events record what users do, such as a signup, and go to an analytics tool. Configure subscribers in `Init`, then call `Track`:

```go
import "github.com/jagreehal/autotel-go/v2/subscribers"

cleanup, err := autotel.Init(ctx,
	autotel.WithService("orders"),
	autotel.WithSubscribers(subscribers.NewPostHogSubscriber("phc_...")),
)

autotel.Track(ctx, "user_signed_up", map[string]any{"user_id": "123", "plan": "premium"})
```

Events tracked inside a span carry its `trace_id` and `span_id`. With no subscribers, `Track` warns once and drops events.

| Destination | Subscriber |
| --- | --- |
| PostHog | `NewPostHogSubscriber(apiKey)` |
| Mixpanel | `NewMixpanelSubscriber(token)` |
| Amplitude | `NewAmplitudeSubscriber(apiKey)` |
| Any HTTP endpoint | `NewWebhookSubscriber(url, WithWebhookHeaders(headers))` |
| A local NDJSON file | `NewFileSubscriber(path)` |
| Memory, for tests | `NewInMemorySubscriber()` |

`TrackFunnelStep`, `TrackFunnelProgression`, `TrackOutcome`, `TrackValue` and `TrackBatch` record common shapes of event. To run a queue outside `Init`, use `subscribers.NewQueue(sub)` with `queue.Track(ctx, name, props)`, and call `queue.Shutdown(ctx)` at exit.

## Update the current span from anywhere

These helpers work on the span in `ctx`, so you don't need to pass the span around:

| To | Call |
| --- | --- |
| Set attributes | `SetAttribute(ctx, key, value)`, `SetAttributes(ctx, attrs)` |
| Add an event | `AddEvent(ctx, name, attrs)` |
| Record an error and mark the span failed | `RecordError(ctx, err, attrs)` |
| Read the IDs for a log or response | `GetTraceID(ctx)`, `GetSpanID(ctx)` |
| Check that tracing is on | `IsTracingEnabled(ctx)` |

These take the span:

| To | Call |
| --- | --- |
| Record elapsed time as `duration_ms` and `duration_ns` | `SetDuration(span, start)` |
| Set method, path and user agent | `SetHTTPRequestAttributes(span, method, path, userAgent)` |
| Add an event from key-value arguments | `AddEventWithAttributes(span, name, "cache.key", "user:123")` |
