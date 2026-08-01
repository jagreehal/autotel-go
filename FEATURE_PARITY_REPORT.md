# Event-Driven OTel Feature Parity Report

**Date**: December 4, 2025
**Purpose**: Coordinate feature parity across Python, Node, and Go autotel implementations

---

## Executive Summary

| Feature | Go | Node | Python |
|---------|:--:|:----:|:------:|
| Producer Tracing | ✅ | ✅ | ✅ |
| Consumer Tracing | ✅ | ✅ | ✅ |
| Batch Processing | ✅ | ✅ | ✅ |
| Links-Based Sampling | ✅ | ✅ | ❌ |
| DLQ/Retry Recording | ✅ | ✅ | ❌ |
| Consumer Lag Metrics | ✅ | ✅ | ❌ |
| Business Baggage | ✅ | ✅ | ❌ |
| Workflow/Saga Tracing | ✅ | ✅ | ❌ |

---

## Detailed Feature Analysis

### 1. Producer Tracing

**Go** (`messaging/producer.go`):
```go
producer := messaging.NewProducer(
    messaging.WithProducerSystem(messaging.SystemKafka),
    messaging.WithProducerDestination("orders"),
)
err := producer.Publish(ctx, &msg, sendFunc)
```

**Node** (`messaging.ts`):
```typescript
const result = await traceProducer(ctx, {
  system: 'kafka',
  destination: 'orders',
}, async (ctx, span) => sendMessage(ctx, msg));
```

**Python** (`messaging.py`):
```python
@trace_producer(system="kafka", destination="orders")
async def send_order(ctx, order):
    await producer.send(order)
```

**Parity**: ✅ All three have equivalent functionality. Go uses middleware pattern, Node uses wrapper function, Python uses decorators.

---

### 2. Consumer Tracing

**Go** (`messaging/consumer.go`):
```go
consumer := messaging.NewConsumer(
    messaging.WithSystem(messaging.SystemKafka),
    messaging.WithDestination("orders"),
    messaging.WithLinks(), // Links mode (default)
)
err := consumer.Process(ctx, msg, handler)
```

**Node** (`messaging.ts`):
```typescript
await traceConsumer(ctx, msg, {
  system: 'kafka',
  destination: 'orders',
  useLinks: true,
}, async (ctx, span) => processMessage(msg));
```

**Python** (`messaging.py`):
```python
@trace_consumer(system="kafka", destination="orders")
async def process_order(ctx, msg):
    # Automatically extracts links from msg.headers
    await handle_order(msg)
```

**Parity**: ✅ All three support automatic link extraction from message headers.

---

### 3. Batch Processing

**Go** (`messaging/batch.go`):
```go
processor := messaging.NewBatchProcessor(
    messaging.WithBatchSystem(messaging.SystemKafka),
    messaging.WithBatchDestination("events"),
)
err := processor.Process(ctx, messages, batchHandler)
// Or process each with individual error handling:
err := processor.ProcessEach(ctx, messages, perMessageHandler)
```

**Node** (`messaging.ts`):
```typescript
await traceConsumer(ctx, messages, {
  batchMode: true,
  // Creates fan-in links from all messages
}, async (ctx, span) => processBatch(messages));
```

**Python** (`messaging.py`):
```python
@trace_batch_consumer(system="kafka", destination="events")
async def process_batch(ctx, messages):
    # Automatic fan-in links
    for msg in messages:
        await process(msg)
```

**Parity**: ✅ All three support batch processing with fan-in links.

---

### 4. Links-Based Sampling

**Go** (`sampling/adaptive.go`, `sampling/links.go`):
```go
// Sampler config
sampler := sampling.NewAdaptiveSampler(
    sampling.WithLinksBased(true),
    sampling.WithLinksRate(1.0), // 100% when linked to sampled span
)

// Create links from headers
link, ok := sampling.CreateLinkFromHeaders(msg.Headers)
ctx, span := tracer.Start(ctx, "process", trace.WithLinks(link))
```

**Node** (`sampling.ts`):
```typescript
// Links-based sampling in tail sampler config
const sampler = createAdaptiveSampler({
  linksBased: true,
  linksRate: 1.0,
});
```

**Python**: ❌ **NOT IMPLEMENTED**

**Recommendation for Python**:
Add links-based sampling to ensure consumer spans are sampled when linked to sampled producer spans. This is critical for maintaining trace continuity in event-driven architectures.

```python
# Suggested API:
sampler = AdaptiveSampler(
    links_based=True,
    links_rate=1.0,
)

# And utility functions:
link = create_link_from_headers(msg.headers)
```

---

### 5. DLQ/Retry Recording

**Go** (`messaging/dlq.go`):
```go
// Record DLQ send
messaging.RecordDLQ(ctx, messaging.DLQInfo{
    OriginalDestination: "orders",
    DLQDestination:      "orders.dlq",
    Reason:              "max_retries_exceeded",
    RetryCount:          3,
})

// Record retry attempt
messaging.RecordRetry(ctx, messaging.RetryInfo{
    Attempt:      2,
    MaxAttempts:  3,
    BackoffMs:    1000,
    LastError:    "timeout",
})
```

**Node** (`messaging.ts`):
```typescript
// Via span helpers
recordDLQ(span, {
  originalDestination: 'orders',
  dlqDestination: 'orders.dlq',
  reason: 'max_retries_exceeded',
});

recordRetry(span, {
  attempt: 2,
  maxAttempts: 3,
});
```

**Python**: ❌ **NOT IMPLEMENTED**

**Recommendation for Python**:
Add helper functions to record DLQ and retry semantics:

```python
# Suggested API:
record_dlq(ctx, original_destination="orders", dlq="orders.dlq", reason="...")
record_retry(ctx, attempt=2, max_attempts=3, backoff_ms=1000)
```

---

### 6. Consumer Lag Metrics

**Go** (`messaging/dlq.go`):
```go
messaging.RecordConsumerLag(ctx, messaging.ConsumerLagInfo{
    LagMs:         1500,
    LagMessages:   100,
    Partition:     0,
    CommittedOffset: 12345,
    HighWatermark:   12445,
})
```

**Node** (`messaging.ts`):
```typescript
recordConsumerLag(span, {
  lagMs: 1500,
  lagMessages: 100,
  partition: 0,
});
```

**Python**: ❌ **NOT IMPLEMENTED**

**Recommendation for Python**:
```python
record_consumer_lag(ctx, lag_ms=1500, lag_messages=100, partition=0)
```

---

### 7. Business Baggage (Safe Context Propagation)

**Go** (`baggage/business.go`):
```go
bc := baggage.New(
    baggage.WithAllowedKeys("tenant_id", "correlation_id", "user_tier"),
    baggage.WithHashKeys("user_id", "email"),  // PII hashing
    baggage.WithMaxValueLength(256),
)

ctx, _ = bc.Set(ctx, "tenant_id", "acme-corp")
ctx, _ = bc.Set(ctx, "user_id", "user@email.com") // Auto-hashed
```

**Node** (`business-baggage.ts`):
```typescript
const schema = defineBusinessBaggage({
  tenantId: { type: 'string', propagate: true },
  userId: { type: 'string', pii: true }, // Auto-hashed
  correlationId: { type: 'string' },
});

ctx = schema.set(ctx, 'tenantId', 'acme-corp');
```

**Python**: ❌ **NOT IMPLEMENTED**

**Recommendation for Python**:
Add safe baggage propagation with:
- Allowlist of keys to prevent baggage explosion
- PII hashing for sensitive fields
- Value length limits
- Type validation

```python
# Suggested API:
bc = BusinessContext(
    allowed_keys=["tenant_id", "correlation_id"],
    hash_keys=["user_id", "email"],
    max_value_length=256,
)

ctx = bc.set(ctx, "tenant_id", "acme-corp")
```

---

### 8. Workflow/Saga Tracing

**Go** (`workflow/workflow.go`):
```go
wf := workflow.New("order-fulfillment", ctx)

wf.Step("validate", func(ctx context.Context, span trace.Span) error {
    return validateOrder(ctx, order)
})

wf.Step("charge", func(ctx context.Context, span trace.Span) error {
    return chargeCustomer(ctx, order)
}, workflow.WithCompensation(func(ctx context.Context, span trace.Span) error {
    return refundCustomer(ctx, order)
}))

err := wf.Run(ctx) // Compensations auto-run on failure
```

**Node** (`workflow.ts`):
```typescript
const workflow = traceWorkflow('order-fulfillment', ctx);

await workflow.step('validate', async (ctx, span) => {
  return validateOrder(order);
});

await workflow.step('charge', async (ctx, span) => {
  return chargeCustomer(order);
}, {
  compensation: async (ctx, span) => {
    return refundCustomer(order);
  }
});

await workflow.complete(); // or workflow.fail(error)
```

**Python**: ❌ **NOT IMPLEMENTED**

**Recommendation for Go and Python**:
Implement workflow/saga tracing with:
- Workflow-level span grouping
- Step tracking with parent links
- Compensation handler registration
- Rollback orchestration
- Workflow state attributes

---

## Priority Recommendations

### For Python Maintainers (High Priority)

1. **Links-Based Sampling** - Critical for event-driven trace continuity
2. **Business Baggage** - Prevents PII leaks and baggage explosion
3. **DLQ/Retry Helpers** - Essential for production debugging
4. **Consumer Lag Recording** - Key observability metric
5. **Workflow/Saga Support** - Complex orchestration tracing

### For Node Maintainers (Complete)

Node appears to have full feature parity. Consider:
- Documenting best practices for each feature
- Adding integration examples with popular frameworks (NestJS, Fastify)

### For Go (Complete)

All event-driven OTel features are now implemented:
1. ✅ Links-Based Sampling - `sampling/adaptive.go`, `sampling/links.go`
2. ✅ Business Baggage - `baggage/business.go`
3. ✅ DLQ/Retry Recording - `messaging/dlq.go`
4. ✅ Consumer Lag Recording - `messaging/dlq.go`
5. ✅ Workflow/Saga Support - `workflow/workflow.go`

---

## API Consistency Recommendations

To ensure best DX across all three languages, align on:

### Naming Conventions

| Concept | Go | Node | Python (Suggested) |
|---------|----|----|-----|
| System constant | `SystemKafka` | `'kafka'` | `SYSTEM_KAFKA` |
| Producer func | `NewProducer()` | `traceProducer()` | `@trace_producer` |
| Consumer func | `NewConsumer()` | `traceConsumer()` | `@trace_consumer` |
| Links mode | `WithLinks()` | `useLinks: true` | `use_links=True` |

### Semantic Attributes (OTel Standard)

All implementations should use official OTel semantic conventions:
- `messaging.system`
- `messaging.destination.name`
- `messaging.operation.name`
- `messaging.message.id`
- `messaging.kafka.consumer.group`
- `messaging.batch.message_count`

---

## Next Steps

1. **Python**: Prioritize links-based sampling and business baggage (see detailed recommendations above)
2. **Node**: Maintain feature parity, add integration examples for NestJS/Fastify
3. **Go**: ✅ Complete - All features implemented
4. **All**: Create integration examples for each major messaging system (Kafka, RabbitMQ, SQS)
