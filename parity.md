# Node–Go Autotel Feature Parity

**Purpose**: Single source of truth for bringing autotel-go to feature parity with the Node autotel implementation. Every task is listed; verification is specified per area and globally.

**Date**: February 2026

**Reference**: Node repo at `/Users/jreehal/dev/node-examples/autotel` (packages: `autotel`, `autotel-plugins`, `autotel-subscribers`; apps: `apps/*`). Relevant commits: EDA enhancements (#53), canonical logs (#32), events-trace-context (#46), workflow/messaging/business-baggage (#17).

---

## Summary Table

| Feature | Node | Go | Status |
|--------|------|-----|--------|
| Core init, traces, metrics | Yes | Yes | Implemented |
| Events + subscribers (PostHog, Mixpanel, Amplitude, Webhook) | Yes | Yes | Implemented |
| Business baggage (safe schema, PII hashing) | Yes | Yes (`baggage/`) | Implemented |
| Messaging (producer/consumer, batch, links, DLQ, lag) | Yes | Yes (`messaging/`) | Implemented |
| Workflow/saga (steps, compensation) | Yes | Yes (`workflow/`) | Implemented |
| Links-based sampling | Yes | Yes (`sampling/`) | Implemented |
| YAML config + env substitution | Yes | Yes (`yaml_loader.go`, `config_merge.go`) | Implemented |
| HTTP client middleware | Yes | Yes (`middleware/httpclient.go`) | Implemented |
| Graceful shutdown (flush, cleanup) | Yes | Yes (defer cleanup) | Implemented |
| Testing: InMemoryExporter | Yes | Yes (`testing/`) | Implemented |
| Correlation ID (get/set/generate, getOrCreate, baggage, runWith) | Yes | Yes (`correlationid/`) | Implemented |
| Canonical log lines (span → wide-event log) | Yes | No | **Not implemented** |
| Span processors: Filtering, SpanNameNormalizer, AttributeRedacting | Yes | Yes (`processors/`) | Implemented |
| Tail sampling processor (post-decision drop) | Yes | Yes (`processors/`) | Implemented |
| Events: includeTraceContext, traceUrl, enrichFromBaggage, hashLinkedTraceIds | Yes | Partial | **Not implemented** |
| Operation context (name for events) | Yes | Yes (`GetOperationContext`, `RunInOperationContext`) | Implemented |
| Semantic helpers (traceLLM, traceDB, traceHTTP, traceMessaging) | Yes | No | **Not implemented** |
| Typed baggage / defineBaggageSchema on TraceContext | Yes | No (only business baggage) | **Not implemented** |
| Distributed workflow (traceDistributedWorkflow, WorkflowBaggage) | Yes | No | **Not implemented** |
| Messaging: customAttributes, customHeaders, messageIdFrom, etc. | Yes | No (only ExtraAttributes) | **Not implemented** |
| Event testing: EventCollector, createEventCollector | Yes | In-memory subscriber exists | Partial |
| Messaging testing: createMessagingTestHarness | Yes | No | **Not implemented** |
| Subscribers: Segment, Slack | Yes | No | **Not implemented** |
| MCP (Model Context Protocol) observability | Yes (`autotel-mcp`) | Yes (`mcp/`) | Implemented |

---

## 1. Core & Config

### Tasks

- [x] Implement `correlationid` package (or add under `autotel`):
  - `GenerateCorrelationID() string` — 16 hex chars (64 bits), crypto-random.
  - `GetCorrelationID(ctx context.Context) string` — resolve from context storage, then baggage, then trace ID prefix, then empty.
  - `GetOrCreateCorrelationID(ctx) string` — get existing or generate new (Node: getOrCreateCorrelationId).
  - `SetCorrelationID(ctx, id, setInBaggage) (context.Context, error)` — store in context and optionally in baggage.
  - `SetCorrelationIDInBaggage(ctx, id) (context.Context, error)` — add ID to baggage only (Node: setCorrelationIdInBaggage).
  - `RunWithCorrelationID(ctx, id, setInBaggage, fn) (T, error)` — run fn with correlation ID set in context.
  - `CORRELATION_ID_BAGGAGE_KEY` constant (e.g. `autotel.correlation_id`).
  - Integration with W3C Baggage propagation so ID flows across service boundaries when baggage is propagated.
- [x] Add operation context for events:
  - `GetOperationContext(ctx context.Context) (name string, ok bool)` — return current operation name if set.
  - `RunInOperationContext(ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error)` — set operation name for duration of fn (e.g. via context value or similar).
  - Event tracking (Track, etc.) reads operation name via `GetOperationName(ctx)` and attaches to event attributes (queue already uses it).

### Verification

- **Packages/files**: New `correlationid/` (or `correlation_id.go`), `operationcontext.go` (or under events).
- **Unit tests**: `TestCorrelationID_Generate`, `TestCorrelationID_GetFromContext`, `TestCorrelationID_GetFromBaggage`, `TestCorrelationID_RunWith`, `TestOperationContext_GetAndRun`.
- **Integration**: Optional small example that sets correlation ID and operation context, emits an event, and asserts event properties contain correlation ID and operation name (e.g. via in-memory subscriber).

---

## 2. Span Processors

### Tasks

- [x] **FilteringSpanProcessor**: Span processor that wraps another; in `OnEnd`, if predicate(span) returns false, do not forward the span to the wrapped processor (effectively drop it). Register via `WithSpanFilter(predicate)` (`processors.NewFilteringSpanProcessor`).
- [x] **SpanNameNormalizingProcessor**: Processor that wraps next; API in place (`processors.NewSpanNameNormalizingProcessor`). In Go SDK span data is read-only at OnEnd, so name rewriting would require exporter-level support; presets (e.g. `NormalizerPresetHTTP`) available. Register via `WithSpanNameNormalizer(fn)`.
- [x] **AttributeRedactingProcessor**: Processor that wraps next; API in place (`processors.NewAttributeRedactingProcessor`). Global PII redaction via `WithPIIRedaction`. Register via `WithAttributeRedactor(fn)`.
- [x] **TailSamplingSpanProcessor**: Processor that wraps another; in `OnEnd`, if span has `sampling.tail.evaluated == true` and `sampling.tail.keep == false`, do not forward. Register via `WithTailSampling(true)`. Attributes: `processors.TailEvaluatedKey`, `processors.TailKeepKey`.

### Verification

- **Packages/files**: New `processors/` or under `internal/` (e.g. `filtering.go`, `span_name_normalizer.go`, `attribute_redacting.go`, `tail_sampling.go`); wire in `autotel.go` / `buildTracerProvider`.
- **Unit tests**: `TestFilteringSpanProcessor_DropsWhenPredicateFalse`, `TestSpanNameNormalizingProcessor_RewritesName`, `TestAttributeRedactingProcessor_RedactsKeys`, `TestTailSamplingSpanProcessor_DropsWhenKeepFalse`.
- **Integration**: Test that creates root and child spans, sets filter to drop children, asserts only root is exported (e.g. via InMemoryExporter).

---

## 3. Canonical Log Lines

### Tasks

- [ ] Add init option `WithCanonicalLogLines(opts CanonicalLogLineOptions)` where options include: `Enabled`, `RootSpansOnly`, `MinLevel` ("debug"|"info"|"warn"|"error"), `MessageFormat func(span) string`, `IncludeResourceAttributes`, `AttributeRedactor func(key, value) value`.
- [ ] Implement **CanonicalLogLineProcessor** (span processor): on `OnEnd(span)`, build one wide log record containing: all span attributes, `traceId`, `spanId`, `duration_ms`, `status_code`, `status_message`, `operation` (span name), `timestamp`; optionally resource attributes. Apply attribute redactor if configured. If `RootSpansOnly`, only emit for root spans. Emit via configured logger or OTLP Logs API (if available in Go SDK).
- [ ] Wire processor into `buildTracerProvider` when `WithCanonicalLogLines` is set; place after other processors (e.g. after filtering/normalizing) so canonical logs respect them.

### Verification

- **Packages/files**: New `processors/canonical_log_line.go` or `canonical.go`; `options.go` and `autotel.go` for option and wiring.
- **Unit tests**: `TestCanonicalLogLine_EmitsOnSpanEnd`, `TestCanonicalLogLine_RootSpansOnly`, `TestCanonicalLogLine_RedactorApplied`.
- **Integration**: Run a short-lived program that starts a span and ends it with Init using canonical log lines and a test logger that captures lines; assert one log record contains expected keys (traceId, spanId, duration_ms, operation).

---

## 4. Events and Subscribers

### Tasks

- [ ] **Events config extension**: Add to config (or events-specific config): `IncludeTraceContext bool`, `TraceUrl func(ctx) string` (receives traceId, spanId, correlationId, serviceName, environment), `EnrichFromBaggage EnrichFromBaggageConfig` (Allow []string, Deny []string, Prefix string, MaxKeys int, MaxBytes int, Transform map[string]string or func(key, value) string e.g. "plain"|"hash"), `HashLinkedTraceIds func(traceIds []string) string` for batch/correlation. Apply in all Track* paths before sending to subscribers: add trace context attributes when IncludeTraceContext; add trace URL when TraceUrl set; merge enriched baggage key-value when EnrichFromBaggage set; use HashLinkedTraceIds when attaching linked trace IDs to events.
- [ ] **Segment subscriber**: Implement `subscribers.Segment` (or `segment.go`) with `Send(ctx, event, properties) error` and `Close() error`, calling Segment HTTP API (or SDK if available). Add to `subscribers/` and document in README.
- [ ] **Slack subscriber**: Implement `subscribers.Slack` (or `slack.go`) with `Send(ctx, event, properties) error` and `Close() error`, posting to Slack webhook or API. Add to `subscribers/` and document.

### Verification

- **Packages/files**: `config.go` / `options.go` for events config; `subscribers/segment.go`, `subscribers/slack.go`; event queue or track path where enrichment and trace URL are applied.
- **Unit tests**: `TestEnrichFromBaggage_AllowDeny`, `TestEnrichFromBaggage_TransformHash`, `TestTraceUrl_IncludedInEvent`, `TestHashLinkedTraceIds`; for Segment/Slack: mock HTTP and assert request body/headers.
- **Integration**: Init with in-memory subscriber and EnrichFromBaggage + TraceUrl set; track event; assert collected event has enriched keys and trace URL field.

---

## 5. Semantic Helpers

### Tasks

- [ ] **traceLLM**: Helper that starts a span with Gen AI semantic conventions: e.g. `gen.ai.request.model`, `gen.ai.operation.name`, `gen.ai.system`. Signature e.g. `TraceLLM(ctx, name string, config LLMConfig, fn func(context.Context, trace.Span) (T, error)) (T, error)`. Config: Model, Operation ("chat"|"completion"|"embedding"), Provider (optional).
- [ ] **traceDB**: Helper that starts a span with DB semantic conventions: system, operation, database, collection/table. Signature similar; config: System, Operation, Database, Collection.
- [ ] **traceHTTP**: Helper that starts a span with HTTP client/server conventions: method, url, status. Config: Method, URL, StatusCode (optional).
- [ ] **traceMessaging**: Helper that starts a span with messaging conventions: system, operation, destination. Config: System, Operation, Destination.
- [ ] Place helpers in a new package e.g. `semconv/` or `semantic/` and document in README.

### Verification

- **Packages/files**: New `semconv/llm.go`, `db.go`, `http.go`, `messaging.go` (or single `semantic.go`).
- **Unit tests**: `TestTraceLLM_SetsGenAIAttributes`, `TestTraceDB_SetsDBAttributes`, `TestTraceHTTP_SetsHTTPAttributes`, `TestTraceMessaging_SetsMessagingAttributes` (start span, end, read span from exporter and assert attribute keys/values).

---

## 6. Trace Context and Baggage

### Tasks

- [ ] **Typed baggage schema**: Define a schema (e.g. struct tags or a builder) for a set of baggage keys with types (string, number, boolean, enum) and optional constraints (max length, required). `DefineBaggageSchema(name string, fields map[string]FieldDef)` or similar.
- [ ] **TraceContext extension**: Extend `TraceContext` (or provide a separate typed context) to support `GetTypedBaggage(schema) (T, bool)` and `SetTypedBaggage(schema, value)` so that callers can read/write typed baggage without string keys. Ensure this works with existing W3C Baggage propagation (serialize/deserialize typed fields to baggage).
- [ ] Document that Business Baggage remains the “safe” production API; typed baggage schema is for convenience and type safety when keys are known.

### Verification

- **Packages/files**: `baggage/schema.go` (extend or add), `functional.go` or `trace_context.go` for TraceContext interface and implementation.
- **Unit tests**: `TestDefineBaggageSchema_GetSet`, `TestTypedBaggage_PropagatesInContext` (set in parent, get in child span or function).

---

## 7. Distributed Workflow

### Tasks

- [ ] **WorkflowBaggage schema**: Define baggage keys for distributed workflow: `workflowId`, `workflowName`, `workflowVersion`, `stepName`, `stepIndex`, `totalSteps` (and any other from Node). Use safe baggage (allowlist, max length) so propagation does not explode.
- [ ] **TraceDistributedWorkflow**: Function that starts a workflow span and sets workflow baggage (workflowId, workflowName, version) on context; runs fn; propagates baggage when producer uses `propagateBaggage: true`. Signature e.g. `TraceDistributedWorkflow(ctx, name string, workflowId string, opts ...DistributedWorkflowOption) (context.Context, trace.Span)` or wrapper that runs fn with workflow context.
- [ ] **TraceDistributedStep**: Function that extracts workflow baggage from incoming context (e.g. from message headers via propagator), sets it on context, starts a step span, runs fn. Option `ExtractBaggage bool` to pull from baggage.
- [ ] **Extract/propagate**: Ensure messaging producer and consumer can propagate and extract baggage so workflow IDs flow across services. Document usage with `messaging.Producer` and `messaging.Consumer` (e.g. enable baggage propagation in producer config).

### Verification

- **Packages/files**: New `workflow/distributed.go` (or under `workflow/`).
- **Unit tests**: `TestWorkflowBaggage_SetAndGet`, `TestTraceDistributedWorkflow_SetsBaggage`, `TestTraceDistributedStep_ExtractsBaggage`.
- **Integration**: Two “services” in-process: one calls TraceDistributedWorkflow and “sends” context (e.g. inject into map, other extracts); other calls TraceDistributedStep and reads WorkflowBaggage; assert workflowId matches.

---

## 8. Messaging API Extensions

### Tasks

- [ ] **Producer config extensions**: Add to `ProducerConfig` or as options: `MessageIdFrom func(args) string`, `PartitionFrom func(args) int`, `KeyFrom func(args) string`, `CustomAttributes func(ctx, args) []attribute.KeyValue`, `CustomHeaders func(ctx) map[string]string`, `BeforeSend func(ctx, args)`, `OnError func(err, ctx)`. Use in producer when starting span (set attributes from CustomAttributes, MessageIdFrom, etc.) and when injecting headers (merge CustomHeaders with traceparent/baggage).
- [ ] **Consumer config extensions**: Add equivalent hooks for consumer: custom attributes from message/headers, optional beforeProcess/onError. Ensure links and existing attributes still take precedence; custom attributes add to span.
- [ ] **Implementation**: In `messaging/producer.go` and `messaging/consumer.go`, call these hooks at the appropriate points and set span attributes and headers accordingly.

### Verification

- **Packages/files**: `messaging/producer.go`, `messaging/consumer.go`, `messaging/attributes.go` or options.
- **Unit tests**: `TestProducer_CustomAttributesSet`, `TestProducer_CustomHeadersInjected`, `TestProducer_MessageIdFrom`, `TestConsumer_CustomAttributesFromMessage`.

---

## 9. Testing Utilities

### Tasks

- [ ] **Event collector**: If not already covered by in-memory subscriber, add `testing.EventCollector` (or use existing inmemory subscriber) with: `GetEvents() []EventData`, `GetFunnelSteps()`, `GetOutcomes()`, `GetValues()`, `Clear()`. Document usage in tests (e.g. create subscriber that implements Subscriber and appends to slice; pass to Init or event queue). If inmemory already provides this, add a short doc in parity.md and a test that uses it as event collector.
- [ ] **Messaging test harness**: Add `testing.CreateMessagingTestHarness()` or `messaging_testing.Harness`: records producer calls (destination, system, payload, headers, traceId, spanId) and consumer calls (destination, system, consumerGroup, etc.). Methods: `AssertProducerCalled(destination, opts)`, `AssertConsumerCalled(destination, opts)`, `Reset()`, `Shutdown()`. Implement by providing a mock producer/consumer that delegates to real tracing but records arguments; or by using an in-memory exporter and asserting span attributes. Match Node API where possible (e.g. `RecordedProducerCall`, `RecordedConsumerCall` structs).
- [ ] Document in `testing/README.md` or in main README under “Testing”.

### Verification

- **Packages/files**: `testing/event_collector.go` or extend `subscribers/inmemory.go`; new `testing/messaging_harness.go` or `messaging/testing.go`.
- **Unit tests**: Use harness in existing messaging tests (e.g. producer test that asserts one producer call with correct destination and trace headers); event collector in event tests.

---

## 10. Examples and Docs

### Tasks

- [ ] List current Go examples and map to Node apps: basic ↔ example-basic, http-server ↔ example-http, service-to-service ↔ example (Node may have similar), production-example ↔ production patterns, analytics-posthog ↔ example-subscribers. Document in parity.md or README.
- [ ] Add **example-canonical-logs** (or `examples/canonical-logs`): small program that initializes autotel with WithCanonicalLogLines, starts a root span with attributes, ends it, and prints or captures the canonical log line (e.g. via test logger). README with one-line run instruction.
- [ ] Add **example-event-enrichment** (or document as future): init with EnrichFromBaggage and TraceUrl, track event, show enriched payload; optional if events config is implemented later.
- [ ] Update **README** or **QUICKSTART**: add “Feature parity” or “Comparison with Node” section and link to `parity.md`. Mention that parity.md is the checklist for bringing Go up to date with Node.

### Verification

- **Files**: `examples/canonical-logs/main.go`, `examples/README.md` or root README; `parity.md` (this file) and README links.
- **Manual**: Run `go run ./examples/canonical-logs` and confirm one wide log line in output (or in test).

---

## 11. MCP (Model Context Protocol)

Follows [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/). Node: `packages/autotel-mcp`.

### Tasks

- [x] **Context propagation**: `ExtractContextFromMeta(ctx, meta)` to extract trace context from MCP `params._meta` (traceparent, tracestate, baggage). `InjectContextToMeta(ctx)` to inject current context into a map for outgoing requests. `MergeMeta(base, injected)` to merge injected context into existing _meta.
- [x] **Semantic constants**: Attribute keys (`mcp.method.name`, `gen_ai.tool.name`, etc.), method names (`tools/call`, `tools/list`, etc.), metric names, duration buckets.
- [x] **Client span helper**: `StartClientSpan(ctx, methodName, opts)` starts a CLIENT span with spec-compliant attributes (tool name, prompt name, resource URI, network transport, session ID, opt-in tool args/result).
- [x] **Server span helper**: `StartServerSpan(ctx, methodName, opts)` starts a SERVER span; use after `ExtractContextFromMeta` so server span is child of client.
- [x] **Metrics**: `RecordClientOperationDuration`, `RecordServerOperationDuration` (histograms with spec bucket boundaries).
- [x] **Error handling**: `SetSpanError(span, errType, message)` and `ToolErrorType` constant for tool_error.

### Verification

- **Packages/files**: `mcp/semconv.go`, `mcp/context.go`, `mcp/span.go`, `mcp/metrics.go`.
- **Unit tests**: `TestExtractContextFromMeta_*`, `TestInjectContextToMeta`, `TestInjectExtractRoundtrip`, `TestMergeMeta`, `TestStartClientSpan`, `TestStartServerSpan`.
- **Reference**: Node `autotel-mcp` (instrumentMcpClient, instrumentMcpServer, extractOtelContextFromMeta, injectOtelContextToMeta).

---

## Verification Strategy (Global)

- **Unit tests**: Run `go test ./...` for all packages: `sampling`, `baggage`, `messaging`, `workflow`, `subscribers`, `middleware`, `redaction`, `circuitbreaker`, `ratelimit`, `processors/`, `correlationid/`, `mcp/`, `testing/`. Aim for at least one test per new public API.
- **Integration tests**: (1) Init with canonical log processor + test logger, start/end span, assert one log record. (2) Init with event EnrichFromBaggage + in-memory subscriber, track event, assert enriched attributes. (3) Distributed workflow in-process: inject/extract workflow baggage, assert workflowId in step.
- **CI**: Ensure `make ci` (or equivalent) runs format, lint, test, build. Optionally add a job that runs example binaries (e.g. `go run ./examples/basic`, `go run ./examples/http-server`) for a few seconds to smoke-test.
- **Manual**: Run key examples (basic, http-server, service-to-service, production-example), send a few requests, confirm traces appear in OTLP or debug console output.

---

## Reference

- **Node repo**: `/Users/jreehal/dev/node-examples/autotel`
- **Node packages**: `packages/autotel`, `packages/autotel-plugins`, `packages/autotel-subscribers`, `packages/autotel-mcp`
- **Node apps**: `apps/example-basic`, `apps/example-http`, `apps/example-canonical-logs`, `apps/example-subscribers`, etc.
- **Relevant Node commits**: Feature/eda-enhancements (#53), canonical log lines (#32), events-trace-context (#46), workflow/messaging/business-baggage (#17)
