# Queues, workflows and events

Use this page when work leaves the request: a message on a queue, a transaction across several steps, or a callback that arrives hours later.

## Trace a producer and consumer

A *producer* publishes a message and a *consumer* processes it. The consumer starts a new trace with a *span link* back to the producer: a pointer between traces, so a slow queue doesn't stretch one trace over hours.

Your message type implements `messaging.Message` (`ID`, `Headers`, `Payload`). To publish, pass anything with `SetHeader(key, value)`, such as `messaging.NewMapHeaderSetter()`.

```go
import "github.com/jagreehal/autotel-go/v2/messaging"

producer := messaging.NewProducer(
	messaging.WithProducerSystem(messaging.SystemKafka),
	messaging.WithProducerDestination("orders"),
)

headers := messaging.NewMapHeaderSetter() // filled in before your function runs
err := producer.Publish(ctx, headers, func(ctx context.Context) error {
	return kafkaWriter.Send(ctx, payload, headers)
})

consumer := messaging.NewConsumer(
	messaging.WithSystem(messaging.SystemKafka),
	messaging.WithDestination("orders"),
	messaging.WithConsumerGroup("order-processor"),
)

err = consumer.Process(ctx, msg, func(ctx context.Context, span trace.Span) error {
	return processOrder(ctx, msg)
})
```

The package has constants for Kafka, RabbitMQ, SQS, SNS, Pub/Sub, Event Hubs, Service Bus, NATS and Redis. For a one-off call, use `messaging.Produce` and `messaging.Consume`.

To handle a batch in one span, linked to every producer, return how many messages failed:

```go
processor := messaging.NewBatchProcessor(
	messaging.WithBatchSystem(messaging.SystemKafka),
	messaging.WithBatchDestination("events"),
)

err := processor.Process(ctx, messages, func(ctx context.Context, span trace.Span) (int, error) {
	return processBatch(ctx, messages)
})
```

`processor.ProcessEach` adds a child span per message.

Sampling can split these traces, because a consumer's trace has its own ID. To keep consumers linked to a kept producer:

```go
cleanup, err := autotel.Init(ctx,
	autotel.WithAdaptiveSampler(
		sampling.WithBaselineRate(0.1),
		sampling.WithLinksBased(true),
		sampling.WithLinksRate(1.0), // keep all spans linked to a kept span
	),
)
```

If you write your own consumer, `sampling.CreateLinkFromHeaders` and `sampling.ExtractLinksFromBatch` build the links from message headers.

## Record retries, dead letters and lag

A *dead letter queue* (DLQ) holds messages you gave up on. These calls annotate the span in `ctx`, so make them inside your handler:

```go
messaging.RecordRetry(ctx, messaging.RetryInfo{Count: 2, BackoffMs: 1000, Reason: "timeout"})

messaging.RecordDLQ(ctx, messaging.DLQInfo{
	QueueName:         "orders-dlq",
	ReasonCategory:    messaging.ClassifyDLQReason(err), // or a constant such as messaging.DLQReasonValidation
	OriginalMessageID: msg.ID(),
	OriginalTopic:     "orders",
	OriginalPartition: partition,
	OriginalOffset:    offset,
	RetryCount:        3,
	ProducerHeaders:   msg.Headers(), // keeps the producer's traceparent
})

messaging.RecordDLQReplay(ctx, messaging.DLQReplayInfo{
	SourceDLQ:     "orders-dlq",
	TargetTopic:   "orders",
	MessageID:     msg.ID(),
	ReplayAttempt: 1,
	ReplayedBy:    "dlq-replay-service",
})

messaging.RecordConsumerLag(ctx, messaging.ConsumerLagInfo{Lag: 100, Partition: 0})
```

To count failures per message and send one to the DLQ after three, pass `messaging.OnError(messaging.NewDLQErrorHandler(3, "orders-dlq", sendToDLQ))` to `NewConsumer`.

## Detect duplicates and gaps

```go
tracker := messaging.NewOrderingTracker(
	messaging.WithDeduplicationWindowSize(5000),
	messaging.WithDeduplicationWindowDuration(10*time.Minute),
)

switch tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
	ID: msg.ID(), Sequence: offset, Partition: partition, Topic: "orders",
}) {
case messaging.OrderingDuplicate:
	return nil // seen it inside the window, so skip it
case messaging.OrderingOutOfOrder:
	logger.WarnContext(ctx, "message out of order")
case messaging.OrderingGap:
	logger.WarnContext(ctx, "messages missing")
}
```

`WithOnDuplicate`, `WithOnOutOfOrder` and `WithOnGap` take callbacks if you prefer them.

## Follow a Kafka consumer group

A *rebalance* is Kafka moving partitions between the consumers in a group. Record it, with heartbeats and per-partition lag, to explain a sudden stall:

```go
tracker := messaging.NewConsumerGroupTracker(
	messaging.WithConsumerGroupID("order-processor"),
	messaging.WithMemberID("consumer-1"),
	messaging.WithOnRebalance(func(event messaging.RebalanceEvent) {
		logger.Info("rebalance", "type", event.Type)
	}),
)

tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
	Type:       messaging.RebalanceAssigned,
	Partitions: []messaging.PartitionAssignment{{Topic: "orders", Partition: 0}},
	Timestamp:  time.Now(),
	Generation: 5,
})

tracker.RecordHeartbeat(ctx, true, 5*time.Millisecond)

tracker.RecordPartitionLag(ctx, messaging.PartitionLagInfo{
	Topic: "orders", Partition: 0, CurrentOffset: 1000, EndOffset: 1050, Lag: 50,
})
```

## Run a saga

A *saga* is a transaction across steps that can't share a database transaction. Each step can name a *compensation*, the action that undoes it. `workflow` runs the steps in order and gives each one a span. If a step fails, it runs the compensations of the steps that succeeded, newest first.

```go
import "github.com/jagreehal/autotel-go/v2/workflow"

wf := workflow.New(ctx, "order-fulfilment")

wf.Step("validate", validateOrder)

wf.Step("charge", chargeCustomer,
	workflow.WithCompensation(refundCustomer),
	workflow.WithRetry(workflow.RetryConfig{
		MaxAttempts: 3, BackoffMs: 100, Multiplier: 2.0, MaxBackoff: 5000,
	}),
	workflow.WithIdempotent(),
	workflow.WithLinkToPrevious(),
)

wf.Step("ship", shipOrder,
	workflow.WithCompensation(cancelShipment),
	workflow.WithLinkTo("validate", "charge"),
)

if err := wf.Run(ctx); err != nil {
	return err // the step's error
}
```

Each step and compensation is a `func(context.Context, trace.Span) error`.

If `ship` fails, `refundCustomer` runs. `cancelShipment` doesn't, because a failed step has nothing to undo. Compensations ignore the cancellation of `ctx`, so set your own deadlines inside them. A failed compensation shows on the span, and `Run` still returns the step's error.

`WithLinkToPrevious` and `WithLinkTo` add span links that show which steps depend on which. `WithIdempotent` records that a step is safe to repeat. It doesn't turn retries on or off.

## Pick up a trace after a callback

A payment provider may call your webhook hours after you started the payment. A *parking lot* stores the trace context when you start, so the callback's span can link back to it:

```go
import "github.com/jagreehal/autotel-go/v2/webhook"

store := webhook.NewInMemoryStore()
lot := webhook.NewParkingLot(store, webhook.WithDefaultTTL(24*time.Hour))

func initiatePayment(ctx context.Context, orderID string) error {
	ctx, span := autotel.Start(ctx, "initiate payment")
	defer span.End()

	err := lot.Park(ctx, "payment:"+orderID, webhook.WithMetadata(map[string]string{
		"order_id": orderID,
	}))
	if err != nil {
		return err
	}
	return createPaymentIntent(ctx, orderID)
}

func handleWebhook(ctx context.Context, orderID string) error {
	ctx, span, parked := lot.RetrieveAndTrace(ctx, "payment:"+orderID, "payment succeeded")
	defer span.End()

	if parked != nil { // nil if nothing was parked or it expired
		logger.InfoContext(ctx, "payment completed", "elapsed_ms", parked.ElapsedMs())
	}
	return fulfilOrder(ctx, orderID)
}
```

`RetrieveAndTrace` removes the entry unless you pass `webhook.WithAutoDelete(false)` to `NewParkingLot`. The in-memory store lives in one process, so implement `webhook.Store` to share entries or survive restarts.

## Carry business context with baggage

*Baggage* is a set of key-value pairs that travels with a request to the next service in a `baggage` header. Because it goes wherever the request goes, the `baggage` package controls which keys you can send and what happens to their values.

Set a profile to allow keys, hash personal data and cap value length:

```go
import "github.com/jagreehal/autotel-go/v2/baggage"

bc := baggage.NewWithProfile(&baggage.Profile{
	AllowedKeys:    map[string]bool{"tenant_id": true, "user_id": true, "user_tier": true},
	HashKeys:       map[string]bool{"user_id": true},
	MaxValueLength: 256,
})

ctx, err := bc.Set(ctx, "tenant_id", "acme-corp")
ctx, err = bc.Set(ctx, "user_id", "user@example.com") // stored as a short SHA-256 hash
```

`Set` drops a key that's off the list and returns no error. `baggage.WithBusinessContext(ctx, "tenant_id", "acme-corp", "region", "eu")` uses the default profile, which allows the `baggage.Key*` constants such as `tenant_id`, `user_id` and `order_id`, and hashes `user_id`. `baggage.WithTenant` and its siblings set one key.

For errors on bad values, define a schema:

```go
schema := baggage.NewSchema(
	baggage.WithMaxTotalSize(8192),
	baggage.WithStrictMode(true), // reject fields you didn't define
).
	DefineStringField("tenant_id", 64, true). // max 64 characters, required
	DefineHashedField("user_id").
	DefineEnumField("priority", []string{"low", "normal", "high"}).
	DefinePIIField("notes") // personal data found here is redacted

sb := baggage.NewSafeBaggage(schema)

ctx, err := sb.Set(ctx, "tenant_id", "acme-corp")

missing := sb.CheckRequiredFields(ctx)                    // required fields not set
detected := schema.DetectPII("Contact: john@example.com") // ["email"]
```

Baggage stays off your spans unless you add [`WithBaggageAttributes`](production.md#keep-one-request-whatever-the-sampler-decides) to `Init`.
