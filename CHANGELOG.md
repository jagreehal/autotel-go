# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Fixed

- **`sampling.WithErrorRate`, `WithSlowThreshold` and `WithSlowRate` had no
  effect.** `AdaptiveSampler` stored all three and never read them:
  `ShouldSample` consulted only the parent decision, links, and the baseline
  rate. A service configured with a 10% baseline and `WithErrorRate(1.0)` — the
  configuration the README recommends — dropped 90% of its errors. Present since
  the options were introduced, and invisible to the test suite because
  `TestAdaptiveSampler_Options` asserted only that the sampler returned a
  non-nil result.

  These decisions cannot be made where they were being configured. A head
  sampler runs when a span starts, and neither the status nor the duration
  exists yet. The rates now travel to a span processor that runs at `OnEnd`:
  `AdaptiveSampler.EndPolicy()` exposes them, `processors.WithTailPolicy`
  applies them, and `Init` wires the two halves together.

- **A span kept for failing now brings its ancestors with it.** Keeping the
  failed span alone left it as an orphan: an OTLP receiver renders that as a root
  span whose parent does not exist, and the trace you opened is missing the part
  you came for — the exact failure chapter 15 of _Observability Engineering_
  warns about. A trace that keeps a failed or slow span is now marked, so spans
  ending after it are kept too. Since a child always ends before its parent, that
  covers the ancestor chain. A sibling that ended before the failure is already
  gone; a tail decision cannot travel backwards. The marks are bounded to 10,000
  traces and expire after five minutes.

### Changed

- **Configuring an error or latency rate now records every span in-process.** A
  span dropped at head never reaches `OnEnd`, so its error cannot be kept; the
  head must see everything for the tail to have anything to decide. The baseline
  is applied at the tail instead, still derived from the trace ID so a routine
  trace is kept or dropped whole. Export volume is unchanged — it follows the
  configured rates — but spans are now built before being dropped locally. This
  applies to the default configuration, which pairs a 10% baseline with
  `ErrorRate: 1.0`. Set the error and slow rates no higher than the baseline to
  restore head-only sampling.
- `processors.NewTailSamplingSpanProcessor` accepts options. An explicit
  `sampling.tail.keep` attribute still wins over any configured policy.

## [2.1.0] - 2026-08-01

### Fixed

- **`backends.HoneycombConfig.SampleRate` had no effect.** It was sent as the
  `x-honeycomb-samplerate` header, which belongs to the Events API and is ignored
  on the OTLP endpoint, so a service configured to keep 1 in 10 spans exported
  all of them at full cost. The rate now configures a real SDK head sampler
  (`ParentBased(TraceIDRatioBased(1/rate))`) and is reported to Honeycomb as the
  `SampleRate` resource attribute so counts are reweighted. A negative rate is
  now rejected at startup instead of producing an invalid sampling ratio.
- `backends.Logfire` accepts a self-hosted `Endpoint` without a cloud `Region`.
  The region check ran unconditionally, so an on-premises install could not be
  configured at all.
- `backends.Collector` defaults to `http://localhost:4317` when `Protocol` is
  gRPC. It always defaulted to the HTTP port 4318, so a gRPC collector preset
  with no explicit endpoint failed to connect.
- **`WithEndpoint` now accepts a URL.** `otlptracehttp.WithEndpoint` stores its
  argument verbatim as the host, so the `http://host:port` form used by the README,
  QUICKSTART, and six of the eight shipped examples produced a mangled target
  (`http://http:%2F%2Flocalhost:4318/v1/metrics`) and made `Init` return an error
  before any telemetry was configured. An endpoint carrying a scheme is now routed
  through `WithEndpointURL`, which also parses the path component that vendor
  presets need, and lets the scheme determine TLS rather than the `Insecure` flag.
  Both `http://host:port/path` and bare `host:port` work. Present since v2.0.0.
  An endpoint carrying a base path (a collector mounted under `/otlp`, a vendor
  gateway path) now has the per-signal path appended, since `Config.Endpoint`
  covers every signal and is therefore the OTLP base endpoint. Without that,
  traces were posted to the base itself rather than `<base>/v1/traces`.
- **`WithSpanFilter` and `WithTailSampling` had no effect.** The config merge
  rebuilt the config from defaults and copied back only a hand-maintained list of
  fields; neither pipeline field was on it, so both options were silently
  discarded before the span processors were built. The merge now carries the
  explicit config wholesale and re-layers only the fields that YAML and
  environment variables also feed, so a newly added field cannot be dropped, and
  a reflection-based test fails if one ever is.
- Corrected the module path to `github.com/jagreehal/autotel-go/v2` and updated
  every internal, example, test, and documentation import. This makes v2
  consumable through Go's semantic import versioning. **v2.0.0 was published
  without the `/v2` suffix and cannot be resolved by `go get`; upgrade to 2.1.0.**
- `workflow.WithRetry` now actually waits between attempts. The backoff was
  computed and reported on the span but never slept, so a configured retry
  hammered the failing dependency with no delay. The wait is cancellable via the
  context passed to `Run`, and `RetryConfig.Jitter` is now honoured.
- `messaging` consumer-group partition numbers are formatted as decimal. They
  were encoded as `string(rune('0'+n))`, so partition 10 reached the backend as
  `":"`, 17 as `"A"` and 200 as `"ø"`.
- `messaging.RecordPartitionLag` records the topic and partition as attribute
  *values*. They were interpolated into the attribute *key*, producing unbounded
  attribute-key cardinality.
- `messaging.NewDLQErrorHandler`'s retry counter is mutex-guarded and bounded. It
  was an unsynchronised map mutated from concurrent consumer callbacks, which
  panics with "concurrent map writes", and it grew without limit.
- `baggage.SafeBaggage` size accounting is per-context rather than a per-instance
  running total, so a long-lived instance no longer starts rejecting every write.
  Cardinality tracking is mutex-guarded.
- `baggage` number fields enforce a bound of `0`. `MinValue`/`MaxValue` are now
  `*float64`; a zero bound was previously indistinguishable from "unset" and
  silently skipped.
- `messaging.BatchProcessor.ProcessEach` returns the joined per-message errors.
  It previously always returned `nil`, so callers could not detect failures.
- `middleware`'s traced `RoundTripper` injects headers into a clone instead of
  mutating the caller's request, per the `http.RoundTripper` contract.
- `webhook.ParkingLot.RetrieveAndTrace`'s documented example did not compile and
  described an error return the method never had. Store failures are now recorded
  on the span as `parking_lot.error` instead of being silently discarded.
- `messaging.ClassifyDLQReason` uses `strings` instead of a hand-rolled ASCII-only
  `toLower`, so it no longer misclassifies non-ASCII error text.

### Changed

- Raised the minimum supported Go version from 1.23 to 1.25 and selected the
  patched Go 1.26.5 toolchain for release builds.
- Updated Gin, OpenTelemetry, gRPC, Zap, protobuf, and transitive dependencies.
- Updated CI to test Go 1.25 and 1.26 and current GitHub Action releases. Lint now
  runs once on the go.mod toolchain against a pinned golangci-lint, and
  `govulncheck` gates the build.
- Outbound HTTP client spans now carry OTel semantic-convention attributes
  (`http.request.method`, `url.full`, `url.path`, `url.scheme`, `server.address`,
  `server.port`, `http.response.status_code`). `url.full` excludes the query
  string and user info, which routinely carry credentials.
- `workflow.New` takes the context first: `New(ctx, name, opts...)`.
- `workflow` compensation runs on a context detached from cancellation, so a saga
  still rolls back when the workflow context is cancelled or times out.
- `slo.Tracker.Record` takes a `context.Context` so metric exemplars link back to
  the originating span.
- `slo.Snapshot.BudgetConsumed` was removed; it always held the same value as
  `BurnRate`.

### Removed

- `WithSpanNameNormalizer`, `WithAttributeRedactor`, and the
  `processors.SpanNameNormalizingProcessor` / `processors.AttributeRedactingProcessor`
  types. Both processors forwarded spans unchanged — span data is read-only at
  `OnEnd` in the Go SDK — so the options silently did nothing while appearing to
  redact or rewrite. Use `WithPIIRedaction` for attribute redaction. These will
  return if and when they can be implemented at the exporter level.
- The internal `parity.md` and `FEATURE_PARITY_REPORT.md` working documents,
  which referenced local developer paths and claimed tests that did not exist.

### Added

- **Typed vendor presets** (`backends/`): `Honeycomb`, `Datadog`, `Grafana`,
  `Logfire`, `Langfuse`, `PostHog`, and `Collector` return a single
  `autotel.Option` carrying the endpoint, protocol, TLS setting, and auth
  headers each vendor expects. Missing credentials or a missing service name
  fail at `Init` rather than exporting into the void, and `ParseHeaders` reads
  the `OTEL_EXPORTER_OTLP_HEADERS` format.
- **`WithBaggageAttributes`** copies baggage entries onto spans as attributes via
  a span processor, with allow-listing and prefixing options from
  `processors.BaggageSpanProcessorOption`.

- **SLO tracking and burn-rate alerts** (`slo/`):
  - Rolling-window good/bad event tracking with SLI and error-budget snapshots
  - Dual-window burn-rate alert evaluation
  - Predictive forecasts limited to four times the recent baseline
  - Injectable clocks and OpenTelemetry outcome/burn-rate metrics
  - Cross-checks against the Chapter 12 predictive burn-alert scenarios

- **MCP observability** (`mcp/`): context propagation, semantic attributes,
  client/server spans, operation metrics, and error handling.
- **Correlation IDs** (`correlationid/`): generation, context and baggage
  propagation, trace fallback, and scoped execution helpers.
- **Span processors** (`processors/`): span filtering and tail-sampling decisions.
- **HTTP client instrumentation** (`middleware/httpclient.go`) with automatic
  trace propagation and semantic-convention request/response attributes.
- **Service-to-service example** demonstrating inbound and outbound trace
  propagation.

- **Consumer Group Tracking** (`messaging/consumer_group.go`):
  - `ConsumerGroupTracker` for tracking Kafka consumer group lifecycle
  - `RecordRebalance()` for assigned/revoked/lost partition events
  - `RecordHeartbeat()` for health monitoring
  - `RecordPartitionLag()` for lag metrics
  - Static group membership support (`WithGroupInstanceID`)

- **Message Ordering & Deduplication** (`messaging/ordering.go`):
  - `OrderingTracker` for sequence tracking and duplicate detection
  - `CheckAndTrack()` returns `OrderingOK`, `OrderingOutOfOrder`, `OrderingDuplicate`, or `OrderingGap`
  - Configurable deduplication window (size and time-based)
  - Per-partition sequence tracking
  - Statistics tracking (`Stats()`)

- **Enhanced DLQ Features** (`messaging/dlq.go`):
  - `DLQReasonCategory` enum for categorizing DLQ routing reasons (validation, timeout, poison, etc.)
  - `ClassifyDLQReason()` auto-classifies errors into categories
  - `DLQReplayInfo` and `RecordDLQReplay()` for tracking DLQ replays
  - Producer span linking via `ProducerHeaders`
  - Dwell time tracking

- **Webhook/Parking Lot Pattern** (`webhook/parking_lot.go`):
  - `ParkingLot` for parking trace context before async operations
  - `Park()` stores trace context with TTL and metadata
  - `RetrieveAndTrace()` retrieves context and creates linked span
  - `Store` interface for pluggable backends (in-memory included)
  - Automatic cleanup of expired contexts

- **Safe Baggage Schema** (`baggage/schema.go`):
  - `Schema` with field type validation (string, number, boolean, enum)
  - PII detection with common patterns (email, phone, SSN, credit card, IP)
  - Auto-redaction and hashing of sensitive fields
  - Cardinality limits per field
  - Total size limits
  - `SafeBaggage` wrapper for validated operations

- **Workflow Step Linking & Retry** (`workflow/workflow.go`):
  - `WithLinkToPrevious()` links step to previous step's span
  - `WithLinkTo(stepNames...)` links to specific steps by name
  - `WithRetry(RetryConfig)` configures retry with exponential backoff
  - `WithIdempotent()` marks step as safe to retry
  - `WithDescription()` adds step documentation

## [2.0.0] - 2025-12-03

### Breaking Changes

- **Renamed `OTLPHeaders` to `Headers`** - The `Config.OTLPHeaders` field is now `Config.Headers`, and `WithOTLPHeaders()` is now `WithHeaders()`. This aligns with the TypeScript package naming.

### Added

- **New event tracking methods** for richer product analytics:
  - `TrackFunnelStep(ctx, funnelName, step, properties)` - Track funnel steps with predefined statuses (`FunnelStarted`, `FunnelCompleted`, `FunnelAbandoned`, `FunnelFailed`)
  - `TrackFunnelProgression(ctx, funnelName, stepName, stepNumber, properties)` - Track custom funnel steps with numeric positions
  - `TrackOutcome(ctx, operationName, outcome, properties)` - Track operation outcomes (`OutcomeSuccess`, `OutcomeFailure`, `OutcomePartial`)
  - `TrackValue(ctx, name, value, properties)` - Track numeric values (revenue, counts, etc.)
  - `TrackBatch(ctx, events)` - Track multiple events at once

- **New types** in `events.go`:
  - `FunnelStatus` - Enum type for funnel step statuses
  - `OutcomeStatus` - Enum type for operation outcomes
  - `Event` - Struct for batch event tracking

## [1.0.0] - 2025-12-03

Initial public release of autotel-go covering the full MVP-through-advanced feature set below. This milestone locks in the telemetry core, safety rails (sampling, rate limiting, circuit breaking, redaction), framework middleware, analytics adapters, and CI/CD guardrails needed for production readiness.
