# Running in production

Control what you keep and what you pay for: sampling, load limits, personal data, log files and runtime metrics.

## Choose how much to keep

autotel keeps every trace until you choose, because dropping data is a cost decision. *Sampling* means keeping some traces and dropping the rest. Pick a sampler below, or set `OTEL_TRACES_SAMPLER` without touching code. A sampler you chose applies in development too, unless you asked for `WithDebug(true)` or `AUTOTEL_DEBUG`, which keep everything.

### Keep a fraction, plus errors and slow spans

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithService("orders"),
	autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0.1), // keep 10% of normal traces
		sampling.WithErrorRate(1.0),    // keep every failed span
	),
)
```

With no options, the adaptive sampler keeps 10% of normal traces, every failed span, and every span of a second or longer. Tune the last with `WithSlowThreshold` and `WithSlowRate`.

The baseline is *head sampling*: the decision comes from the trace ID, so a routine trace is kept or dropped whole.

Errors and slow spans need *tail sampling*, a decision made after a span ends, because status and duration don't exist when it starts. So `Init` records every span in your process and applies these rates as each span ends. You export what your rates say, and you pay CPU to build spans you then drop. To decide everything at the start instead, set `WithErrorRate` and `WithSlowRate` no higher than the baseline.

A kept failure brings along the spans of its trace that end after it, such as its parents. Spans that ended earlier are gone, and so are spans in other services, because a tail decision can't reach back to them.

### Keep a set number of traces per second

autotel adjusts the rate to your budget as traffic moves:

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithTargetRateSampler(
		sampling.WithTargetSpansPerSecond(10),
		// One budget per route, so a noisy endpoint can't spend a rare one's allowance.
		sampling.WithSamplingKey(sampling.KeyByAttributes("http.route")),
	),
)
```

The rate is recomputed once a minute (`WithAdjustInterval`), so a change in traffic takes one interval to show. `WithMaxSamplingKeys` caps how many keys autotel tracks, so a key with endless values costs accuracy and leaves memory alone.

This decides at span start, so it can't see errors or latency. Use the adaptive sampler for those.

## Limit spans under load

`WithRateLimit` caps how fast new traces start:

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithRateLimit(100, 200), // 100 per second, bursts of 200
)
```

The limit counts spans without a kept parent. A child of a kept span always passes, so a trace isn't cut in half.

`WithCircuitBreaker(5, 3, 30*time.Second)` stops starting new traces while your backend is failing, so your service doesn't spend time building spans nobody receives. After 5 failed span exports it opens. After 30 seconds it lets traffic through to test the backend, and 3 good exports close it again.

## Keep personal data out

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithPIIRedaction(
		redaction.WithAllowlistKeys("user_id", "order_id"), // never redact these
	),
)
```

`WithPIIRedaction` scrubs:

- Spans from any source, including middleware: names, attributes, events and status.
- Logs from `autotel.Logger`: OTLP, stderr and log files.
- Metrics recorded through `autotel.Meter()`. Two users' values merge into one redacted series.
- Product events sent through `autotel.Track` and its helpers.

It can't see instruments you build yourself with `otel.Meter`, or a `logger.With` value on stderr or in log files if you call `With` before `Init`.

Redaction matches patterns, so it catches common formats and misses the rest. Add your own with `redaction.WithCustomPattern`, and keep personal data out of telemetry where you can.

## Write logs to files

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithLogFile(autotel.LogFile{
		Dir:      "logs",
		Rotation: autotel.LogRotationHourly,
		MaxFiles: 24,
	}),
)
```

`autotel.Logger` output also goes to files such as `logs/orders.2026-11-04-15.log`, as JSON lines with `trace_id` and `span_id`. Names use UTC, and only the owner and group can read the files. Writes are synchronous, so no line is dropped and a slow disk slows the caller.

`AUTOTEL_LOG_FILE_DIR` does the same without code. Give each service its own directory or `Prefix`, because pruning goes by file name. `MaxFiles: 0` means the default of 7. Use `autotel.KeepAllLogFiles` to keep them all.

## See the Go runtime

`autotel.WithRuntimeMetrics()` reports memory, garbage collection, goroutines and scheduler latency. It needs a metrics pipeline, so it does nothing without an endpoint or `WithMetricExporters`.

## Keep one request whatever the sampler decides

With `WithDebugCapture()` in `Init`, a request carrying `autotel.debug` baggage keeps every span in every service it reaches, whatever the sampler or tail policy decide. *Baggage* is a set of key-value pairs that travels with a request between services. The rate limiter and circuit breaker still apply.

Set it at the edge for one request:

```go
ctx, err := autotel.SetBaggage(ctx, autotel.DebugBaggageKey, "ticket-4411")
```

Callers control baggage, so enable this on services whose callers you trust: behind a gateway that sets or strips the key.

`SetBaggage` sets any W3C baggage member. To copy baggage onto every span, in this service and downstream, add `autotel.WithBaggageAttributes(processors.WithBaggagePrefix(""))`. Where callers are untrusted, pass `processors.WithBaggageAllowlist` to copy only the keys you expect.
