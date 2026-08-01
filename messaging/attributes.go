// Package messaging provides tracing primitives for event-driven architectures.
// It implements OpenTelemetry semantic conventions for messaging systems like
// Kafka, RabbitMQ, SQS, and others.
package messaging

import "go.opentelemetry.io/otel/attribute"

// Messaging system identifiers (messaging.system)
const (
	SystemKafka      = "kafka"
	SystemRabbitMQ   = "rabbitmq"
	SystemSQS        = "aws_sqs"
	SystemSNS        = "aws_sns"
	SystemPubSub     = "gcp_pubsub"
	SystemEventHub   = "azure_eventhubs"
	SystemServiceBus = "azure_servicebus"
	SystemNATS       = "nats"
	SystemRedis      = "redis"
)

// Messaging operations (messaging.operation)
const (
	OperationPublish = "publish"
	OperationReceive = "receive"
	OperationProcess = "process"
)

// Standard OTel messaging attribute keys
const (
	// Core messaging attributes
	AttrSystem          = "messaging.system"
	AttrDestinationName = "messaging.destination.name"
	AttrOperation       = "messaging.operation"
	AttrMessageID       = "messaging.message.id"
	AttrConversationID  = "messaging.message.conversation_id"
	AttrPayloadSize     = "messaging.message.payload_size_bytes"
	AttrClientID        = "messaging.client_id"

	// Kafka-specific attributes
	AttrKafkaPartition     = "messaging.kafka.partition"
	AttrKafkaOffset        = "messaging.kafka.offset"
	AttrKafkaConsumerGroup = "messaging.kafka.consumer.group"
	AttrKafkaMessageKey    = "messaging.kafka.message.key"
	AttrKafkaTombstone     = "messaging.kafka.message.tombstone"

	// Consumer lag attributes (custom, practical for observability)
	AttrKafkaConsumerLag = "messaging.kafka.consumer_lag"
	AttrKafkaCommitLag   = "messaging.kafka.commit_lag_ms"

	// Retry attributes
	AttrRetryCount     = "messaging.retry.count"
	AttrRetryBackoffMs = "messaging.retry.backoff_ms"
	AttrRetryReason    = "messaging.retry.reason"

	// Dead Letter Queue attributes
	AttrDLQName        = "messaging.dlq.name"
	AttrDLQReason      = "messaging.dlq.reason"
	AttrDLQOriginalMsg = "messaging.dlq.original_message_id"

	// Batch processing attributes
	AttrBatchSize        = "messaging.batch.size"
	AttrBatchFailedCount = "messaging.batch.failed_count"
	AttrBatchMessageIDs  = "messaging.batch.message_ids"

	// Workflow/Saga attributes
	AttrWorkflowID   = "workflow.id"
	AttrWorkflowName = "workflow.name"
	AttrWorkflowStep = "workflow.step"
	AttrSagaID       = "saga.id"
	AttrSagaName     = "saga.name"
	AttrSagaStep     = "saga.step"

	// Idempotency attributes
	AttrIdempotencyKey = "messaging.idempotency.key"
	AttrDuplicateMsg   = "messaging.idempotency.duplicate"

	// Outbox pattern attributes
	AttrOutboxTxID      = "messaging.outbox.transaction_id"
	AttrOutboxPersisted = "messaging.outbox.persisted"
)

// Attribute helper functions for type-safe attribute creation

// System returns the messaging.system attribute
func System(system string) attribute.KeyValue {
	return attribute.String(AttrSystem, system)
}

// Destination returns the messaging.destination.name attribute
func Destination(name string) attribute.KeyValue {
	return attribute.String(AttrDestinationName, name)
}

// Operation returns the messaging.operation attribute
func Operation(op string) attribute.KeyValue {
	return attribute.String(AttrOperation, op)
}

// MessageID returns the messaging.message.id attribute
func MessageID(id string) attribute.KeyValue {
	return attribute.String(AttrMessageID, id)
}

// PayloadSize returns the messaging.message.payload_size_bytes attribute
func PayloadSize(bytes int) attribute.KeyValue {
	return attribute.Int(AttrPayloadSize, bytes)
}

// KafkaPartition returns the messaging.kafka.partition attribute
func KafkaPartition(partition int) attribute.KeyValue {
	return attribute.Int(AttrKafkaPartition, partition)
}

// KafkaOffset returns the messaging.kafka.offset attribute
func KafkaOffset(offset int64) attribute.KeyValue {
	return attribute.Int64(AttrKafkaOffset, offset)
}

// KafkaConsumerGroup returns the messaging.kafka.consumer.group attribute
func KafkaConsumerGroup(group string) attribute.KeyValue {
	return attribute.String(AttrKafkaConsumerGroup, group)
}

// KafkaMessageKey returns the messaging.kafka.message.key attribute
func KafkaMessageKey(key string) attribute.KeyValue {
	return attribute.String(AttrKafkaMessageKey, key)
}

// KafkaConsumerLag returns the messaging.kafka.consumer_lag attribute
func KafkaConsumerLag(lag int64) attribute.KeyValue {
	return attribute.Int64(AttrKafkaConsumerLag, lag)
}

// RetryCount returns the messaging.retry.count attribute
func RetryCount(count int) attribute.KeyValue {
	return attribute.Int(AttrRetryCount, count)
}

// RetryBackoff returns the messaging.retry.backoff_ms attribute
func RetryBackoff(ms int64) attribute.KeyValue {
	return attribute.Int64(AttrRetryBackoffMs, ms)
}

// RetryReason returns the messaging.retry.reason attribute
func RetryReason(reason string) attribute.KeyValue {
	return attribute.String(AttrRetryReason, reason)
}

// DLQName returns the messaging.dlq.name attribute
func DLQName(name string) attribute.KeyValue {
	return attribute.String(AttrDLQName, name)
}

// DLQReason returns the messaging.dlq.reason attribute (should be non-PII)
func DLQReason(reason string) attribute.KeyValue {
	return attribute.String(AttrDLQReason, reason)
}

// BatchSize returns the messaging.batch.size attribute
func BatchSize(size int) attribute.KeyValue {
	return attribute.Int(AttrBatchSize, size)
}

// BatchFailedCount returns the messaging.batch.failed_count attribute
func BatchFailedCount(count int) attribute.KeyValue {
	return attribute.Int(AttrBatchFailedCount, count)
}

// WorkflowID returns the workflow.id attribute
func WorkflowID(id string) attribute.KeyValue {
	return attribute.String(AttrWorkflowID, id)
}

// WorkflowName returns the workflow.name attribute
func WorkflowName(name string) attribute.KeyValue {
	return attribute.String(AttrWorkflowName, name)
}

// WorkflowStep returns the workflow.step attribute
func WorkflowStep(step string) attribute.KeyValue {
	return attribute.String(AttrWorkflowStep, step)
}

// IdempotencyKey returns the messaging.idempotency.key attribute
func IdempotencyKey(key string) attribute.KeyValue {
	return attribute.String(AttrIdempotencyKey, key)
}

// IsDuplicate returns the messaging.idempotency.duplicate attribute
func IsDuplicate(duplicate bool) attribute.KeyValue {
	return attribute.Bool(AttrDuplicateMsg, duplicate)
}
