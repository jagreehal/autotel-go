# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

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
