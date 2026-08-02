package messaging

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// DLQReason represents the category of DLQ routing reason.
type DLQReasonCategory string

const (
	// DLQReasonValidation indicates the message failed validation (schema, format, etc.)
	DLQReasonValidation DLQReasonCategory = "validation"

	// DLQReasonProcessing indicates a processing error occurred
	DLQReasonProcessing DLQReasonCategory = "processing"

	// DLQReasonTimeout indicates the message timed out during processing
	DLQReasonTimeout DLQReasonCategory = "timeout"

	// DLQReasonPoison indicates a poison message that repeatedly fails
	DLQReasonPoison DLQReasonCategory = "poison"

	// DLQReasonUnknown indicates an unknown or unclassified error
	DLQReasonUnknown DLQReasonCategory = "unknown"

	// DLQReasonMaxRetries indicates max retries exceeded
	DLQReasonMaxRetries DLQReasonCategory = "max_retries_exceeded"

	// DLQReasonDeserialization indicates message couldn't be deserialized
	DLQReasonDeserialization DLQReasonCategory = "deserialization"

	// DLQReasonDependency indicates a dependency failure (database, external service)
	DLQReasonDependency DLQReasonCategory = "dependency_failure"
)

// DLQInfo contains information about a dead letter queue operation.
type DLQInfo struct {
	QueueName         string
	Reason            string            // Non-PII reason for DLQ
	ReasonCategory    DLQReasonCategory // Categorized reason for filtering
	OriginalMessageID string
	RetryCount        int
	ProducerHeaders   map[string]string // Headers from original message for linking
	DwellTimeMs       int64             // Time message spent in original queue
	OriginalTopic     string            // Original topic/queue name
	OriginalPartition int               // Original partition (-1 if N/A)
	OriginalOffset    int64             // Original offset (-1 if N/A)
}

// RecordDLQ records a dead letter queue event on the current span.
// Use this when sending a message to a DLQ after processing failures.
//
// Example:
//
//	err := consumer.Process(ctx, msg, func(ctx context.Context, span trace.Span) error {
//	    err := processOrder(ctx, msg)
//	    if err != nil && retryCount >= maxRetries {
//	        messaging.RecordDLQ(ctx, messaging.DLQInfo{
//	            QueueName:         "orders-dlq",
//	            Reason:            "max_retries_exceeded",
//	            ReasonCategory:    messaging.DLQReasonMaxRetries,
//	            OriginalMessageID: msg.ID(),
//	            RetryCount:        retryCount,
//	            ProducerHeaders:   msg.Headers(), // For linking
//	        })
//	        return sendToDLQ(ctx, msg)
//	    }
//	    return err
//	})
func RecordDLQ(ctx context.Context, info DLQInfo) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		DLQName(info.QueueName),
	}

	if info.Reason != "" {
		attrs = append(attrs, DLQReason(info.Reason))
	}

	if info.ReasonCategory != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.reason_category", string(info.ReasonCategory)))
	}

	if info.OriginalMessageID != "" {
		attrs = append(attrs, attribute.String(AttrDLQOriginalMsg, info.OriginalMessageID))
	}

	if info.RetryCount > 0 {
		attrs = append(attrs, RetryCount(info.RetryCount))
	}

	if info.DwellTimeMs > 0 {
		attrs = append(attrs, attribute.Int64("messaging.dlq.dwell_time_ms", info.DwellTimeMs))
	}

	if info.OriginalTopic != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.original_topic", info.OriginalTopic))
	}

	if info.OriginalPartition >= 0 {
		attrs = append(attrs, attribute.Int("messaging.dlq.original_partition", info.OriginalPartition))
	}

	if info.OriginalOffset >= 0 {
		attrs = append(attrs, attribute.Int64("messaging.dlq.original_offset", info.OriginalOffset))
	}

	// Links can only be attached at span creation time in OTel Go, so the
	// producer's trace context is recorded as an attribute instead.
	if traceparent := info.ProducerHeaders["traceparent"]; traceparent != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.producer_traceparent", traceparent))
	}

	span.SetAttributes(attrs...)
	span.AddEvent("message.sent_to_dlq", trace.WithAttributes(attrs...))
}

// RecordDLQWithLink records a DLQ event and creates a link to the producer span.
// This is useful when you have access to the producer span context directly.
func RecordDLQWithLink(ctx context.Context, info DLQInfo, producerSpanCtx trace.SpanContext) {
	RecordDLQ(ctx, info)

	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	// Record producer trace info as attributes since links can't be added post-creation
	if producerSpanCtx.IsValid() {
		span.SetAttributes(
			attribute.String("messaging.dlq.producer_trace_id", producerSpanCtx.TraceID().String()),
			attribute.String("messaging.dlq.producer_span_id", producerSpanCtx.SpanID().String()),
		)
	}
}

// RetryInfo contains information about a message retry.
type RetryInfo struct {
	Count     int
	BackoffMs int64
	Reason    string
}

// RecordRetry records retry information on the current span.
//
// Example:
//
//	messaging.RecordRetry(ctx, messaging.RetryInfo{
//	    Count:     3,
//	    BackoffMs: 5000,
//	    Reason:    "transient_error",
//	})
func RecordRetry(ctx context.Context, info RetryInfo) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		RetryCount(info.Count),
	}

	if info.BackoffMs > 0 {
		attrs = append(attrs, RetryBackoff(info.BackoffMs))
	}

	if info.Reason != "" {
		attrs = append(attrs, RetryReason(info.Reason))
	}

	span.SetAttributes(attrs...)
	span.AddEvent("message.retry", trace.WithAttributes(attrs...))
}

// ConsumerLagInfo contains consumer lag metrics.
type ConsumerLagInfo struct {
	Lag         int64 // Number of messages behind
	Partition   int
	CommitLagMs int64 // Time since last commit
}

// RecordConsumerLag records consumer lag information on the current span.
// This is essential for diagnosing slow consumers and backpressure issues.
//
// Example:
//
//	// After receiving a message, record the lag
//	messaging.RecordConsumerLag(ctx, messaging.ConsumerLagInfo{
//	    Lag:       highWaterMark - currentOffset,
//	    Partition: partition,
//	})
func RecordConsumerLag(ctx context.Context, info ConsumerLagInfo) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		KafkaConsumerLag(info.Lag),
	}

	if info.Partition >= 0 {
		attrs = append(attrs, KafkaPartition(info.Partition))
	}

	if info.CommitLagMs > 0 {
		attrs = append(attrs, attribute.Int64(AttrKafkaCommitLag, info.CommitLagMs))
	}

	span.SetAttributes(attrs...)
}

// KafkaMessageInfo contains Kafka-specific message metadata.
type KafkaMessageInfo struct {
	Partition int
	Offset    int64
	Key       string
	Tombstone bool
}

// RecordKafkaMessage records Kafka-specific message attributes.
//
// Example:
//
//	messaging.RecordKafkaMessage(ctx, messaging.KafkaMessageInfo{
//	    Partition: msg.Partition,
//	    Offset:    msg.Offset,
//	    Key:       string(msg.Key),
//	})
func RecordKafkaMessage(ctx context.Context, info KafkaMessageInfo) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		KafkaPartition(info.Partition),
		KafkaOffset(info.Offset),
	}

	if info.Key != "" {
		attrs = append(attrs, KafkaMessageKey(info.Key))
	}

	if info.Tombstone {
		attrs = append(attrs, attribute.Bool(AttrKafkaTombstone, true))
	}

	span.SetAttributes(attrs...)
}

// DLQHandler is a callback type for handling DLQ operations.
type DLQHandler func(ctx context.Context, msg Message, err error, retryCount int) error

// NewDLQErrorHandler creates an OnError callback that sends messages to DLQ
// after a maximum number of retries.
//
// Example:
//
//	consumer := messaging.NewConsumer(
//	    messaging.WithSystem(messaging.SystemKafka),
//	    messaging.WithDestination("orders"),
//	    messaging.OnError(messaging.NewDLQErrorHandler(3, "orders-dlq", sendToDLQ)),
//	)
//
// The handler cannot observe successful deliveries, so entries for messages that
// fail once and then succeed are never explicitly cleared. The counter map is
// bounded and evicts oldest-first to keep memory flat on long-running consumers.
const dlqRetryTrackerMaxEntries = 10000

func NewDLQErrorHandler(maxRetries int, dlqName string, dlqSender func(ctx context.Context, msg Message) error) func(ctx context.Context, msg Message, err error) {
	var (
		mu       sync.Mutex
		counts   = make(map[string]int)
		inserted []string // insertion order, for bounded FIFO eviction
	)

	return func(ctx context.Context, msg Message, err error) {
		msgID := msg.ID()

		mu.Lock()
		if _, seen := counts[msgID]; !seen {
			if len(inserted) >= dlqRetryTrackerMaxEntries {
				delete(counts, inserted[0])
				inserted = inserted[1:]
			}
			inserted = append(inserted, msgID)
		}
		counts[msgID]++
		count := counts[msgID]
		exhausted := count >= maxRetries
		if exhausted {
			delete(counts, msgID)
			for i, id := range inserted {
				if id == msgID {
					inserted = append(inserted[:i], inserted[i+1:]...)
					break
				}
			}
		}
		mu.Unlock()

		RecordRetry(ctx, RetryInfo{
			Count:  count,
			Reason: err.Error(),
		})

		if !exhausted {
			return
		}

		RecordDLQ(ctx, DLQInfo{
			QueueName:         dlqName,
			Reason:            "max_retries_exceeded",
			ReasonCategory:    DLQReasonMaxRetries,
			OriginalMessageID: msgID,
			RetryCount:        count,
			ProducerHeaders:   msg.Headers(),
		})

		if dlqSender != nil {
			_ = dlqSender(ctx, msg)
		}
	}
}

// DLQReplayInfo contains information about a DLQ replay operation.
type DLQReplayInfo struct {
	SourceDLQ     string    // Source DLQ name
	TargetTopic   string    // Target topic for replay
	MessageID     string    // Message ID being replayed
	OriginalDLQAt time.Time // When message was originally sent to DLQ
	ReplayAttempt int       // Which replay attempt this is
	DwellTimeMs   int64     // Total time in DLQ
	ReplayedBy    string    // Who/what triggered the replay (service name, user, etc.)
	ReplayBatchID string    // If part of a batch replay
}

// RecordDLQReplay records a DLQ replay operation.
// Use this when replaying messages from a DLQ back to the original topic.
//
// Example:
//
//	messaging.RecordDLQReplay(ctx, messaging.DLQReplayInfo{
//	    SourceDLQ:     "orders-dlq",
//	    TargetTopic:   "orders",
//	    MessageID:     msg.ID(),
//	    OriginalDLQAt: msg.DLQTimestamp(),
//	    ReplayAttempt: 1,
//	    ReplayedBy:    "dlq-replay-service",
//	})
func RecordDLQReplay(ctx context.Context, info DLQReplayInfo) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("messaging.dlq.replay.source", info.SourceDLQ),
		attribute.String("messaging.dlq.replay.target", info.TargetTopic),
	}

	if info.MessageID != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.replay.message_id", info.MessageID))
	}

	if !info.OriginalDLQAt.IsZero() {
		attrs = append(attrs, attribute.Int64("messaging.dlq.replay.original_dlq_at", info.OriginalDLQAt.UnixMilli()))
	}

	if info.ReplayAttempt > 0 {
		attrs = append(attrs, attribute.Int("messaging.dlq.replay.attempt", info.ReplayAttempt))
	}

	if info.DwellTimeMs > 0 {
		attrs = append(attrs, attribute.Int64("messaging.dlq.replay.dwell_time_ms", info.DwellTimeMs))
	}

	if info.ReplayedBy != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.replay.initiated_by", info.ReplayedBy))
	}

	if info.ReplayBatchID != "" {
		attrs = append(attrs, attribute.String("messaging.dlq.replay.batch_id", info.ReplayBatchID))
	}

	span.SetAttributes(attrs...)
	span.AddEvent("message.dlq_replay", trace.WithAttributes(attrs...))
}

// ClassifyDLQReason attempts to classify an error into a DLQ reason category.
// This is a helper for consistent categorization.
func ClassifyDLQReason(err error) DLQReasonCategory {
	if err == nil {
		return DLQReasonUnknown
	}

	errMsg := err.Error()

	// Common patterns - extend as needed
	switch {
	case contains(errMsg, "timeout", "deadline", "context canceled"):
		return DLQReasonTimeout
	case contains(errMsg, "validation", "invalid", "schema", "format"):
		return DLQReasonValidation
	case contains(errMsg, "unmarshal", "deserialize", "decode", "parse"):
		return DLQReasonDeserialization
	case contains(errMsg, "connection", "unavailable", "service", "database", "redis"):
		return DLQReasonDependency
	default:
		return DLQReasonProcessing
	}
}

// contains checks if s contains any of the substrings (case-insensitive).
func contains(s string, substrings ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrings {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
