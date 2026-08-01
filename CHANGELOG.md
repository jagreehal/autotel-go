# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [2.1.0] - 2026-08-01

### Fixed

- Corrected the module path to `github.com/jagreehal/autotel-go/v2` and updated
  every internal, example, test, and documentation import. This makes v2
  consumable through Go's semantic import versioning.

### Changed

- Raised the minimum supported Go version from 1.23 to 1.25 and selected the
  patched Go 1.26.5 toolchain for release builds.
- Updated Gin, OpenTelemetry, gRPC, Zap, protobuf, and transitive dependencies.
- Updated CI to test Go 1.25 and 1.26 and current GitHub Action releases.

### Added

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
- **Span processors** (`processors/`): filtering, tail-sampling decisions,
  attribute-redaction hooks, and span-name normalization hooks.
- **HTTP client instrumentation** (`middleware/httpclient.go`) with automatic
  trace propagation and configurable request/response attributes.
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
