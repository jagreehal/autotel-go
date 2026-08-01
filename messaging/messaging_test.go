package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestMessage implements the Message interface for testing
type TestMessage struct {
	id      string
	headers map[string]string
	payload []byte
}

func (m *TestMessage) ID() string                 { return m.id }
func (m *TestMessage) Headers() map[string]string { return m.headers }
func (m *TestMessage) Payload() []byte            { return m.payload }

func NewTestMessage(id string) *TestMessage {
	return &TestMessage{
		id:      id,
		headers: make(map[string]string),
		payload: []byte("test payload"),
	}
}

func setupTestTracer(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)

	return exporter, func() {
		_ = tp.Shutdown(context.Background())
	}
}

func TestConsumer_Process(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	consumer := NewConsumer(
		WithSystem(SystemKafka),
		WithDestination("orders"),
		WithConsumerGroup("order-processor"),
	)

	msg := NewTestMessage("msg-123")

	var handlerCalled bool
	err := consumer.Process(context.Background(), msg, func(ctx context.Context, span trace.Span) error {
		handlerCalled = true
		span.SetAttributes(attribute.String("custom.attr", "value"))
		return nil
	})

	require.NoError(t, err)
	assert.True(t, handlerCalled)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "kafka.consume orders", span.Name)
	assert.Equal(t, trace.SpanKindConsumer, span.SpanKind)

	// Check attributes
	attrMap := make(map[string]any)
	for _, attr := range span.Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, "kafka", attrMap[AttrSystem])
	assert.Equal(t, "orders", attrMap[AttrDestinationName])
	assert.Equal(t, "process", attrMap[AttrOperation])
	assert.Equal(t, "msg-123", attrMap[AttrMessageID])
	assert.Equal(t, "order-processor", attrMap[AttrKafkaConsumerGroup])
}

func TestConsumer_Process_WithError(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	consumer := NewConsumer(
		WithSystem(SystemKafka),
		WithDestination("orders"),
	)

	msg := NewTestMessage("msg-456")
	expectedErr := errors.New("processing failed")

	var errorCallbackCalled bool
	consumer.config.OnError = func(ctx context.Context, m Message, err error) {
		errorCallbackCalled = true
		assert.Equal(t, expectedErr, err)
	}

	err := consumer.Process(context.Background(), msg, func(ctx context.Context, span trace.Span) error {
		return expectedErr
	})

	assert.Equal(t, expectedErr, err)
	assert.True(t, errorCallbackCalled)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Check error was recorded
	assert.NotEmpty(t, spans[0].Events)
}

func TestConsumer_Process_WithLinks(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	consumer := NewConsumer(
		WithSystem(SystemKafka),
		WithDestination("orders"),
		WithLinks(),
	)

	// Create message with producer trace context
	msg := &TestMessage{
		id: "msg-789",
		headers: map[string]string{
			"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		},
		payload: []byte("test"),
	}

	err := consumer.Process(context.Background(), msg, func(ctx context.Context, span trace.Span) error {
		return nil
	})

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Check link was created
	assert.Len(t, spans[0].Links, 1)
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", spans[0].Links[0].SpanContext.TraceID().String())
}

func TestProducer_Publish(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	producer := NewProducer(
		WithProducerSystem(SystemKafka),
		WithProducerDestination("orders"),
	)

	headers := NewMapHeaderSetter()

	var sendCalled bool
	err := producer.Publish(context.Background(), headers, func(ctx context.Context) error {
		sendCalled = true
		return nil
	})

	require.NoError(t, err)
	assert.True(t, sendCalled)

	// Check headers were injected
	assert.NotEmpty(t, headers["traceparent"])

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "kafka.publish orders", span.Name)
	assert.Equal(t, trace.SpanKindProducer, span.SpanKind)
}

func TestProducer_PublishWithID(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	producer := NewProducer(
		WithProducerSystem(SystemKafka),
		WithProducerDestination("orders"),
	)

	headers := NewMapHeaderSetter()

	err := producer.PublishWithID(context.Background(), "order-123", headers, func(ctx context.Context) error {
		return nil
	})

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Check message ID attribute
	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}
	assert.Equal(t, "order-123", attrMap[AttrMessageID])
}

func TestBatchProcessor_Process(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	processor := NewBatchProcessor(
		WithBatchSystem(SystemKafka),
		WithBatchDestination("orders"),
		WithSampleIDs(2),
	)

	messages := []Message{
		NewTestMessage("msg-1"),
		NewTestMessage("msg-2"),
		NewTestMessage("msg-3"),
	}

	err := processor.Process(context.Background(), messages, func(ctx context.Context, span trace.Span) (int, error) {
		return 1, nil // 1 failed
	})

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]

	attrMap := make(map[string]any)
	for _, attr := range span.Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, int64(3), attrMap[AttrBatchSize])
	assert.Equal(t, int64(1), attrMap[AttrBatchFailedCount])
}

func TestBatchProcessor_ProcessEach(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	processor := NewBatchProcessor(
		WithBatchSystem(SystemKafka),
		WithBatchDestination("orders"),
	)

	messages := []Message{
		NewTestMessage("msg-1"),
		NewTestMessage("msg-2"),
	}

	var processedCount int
	err := processor.ProcessEach(context.Background(), messages, func(ctx context.Context, span trace.Span, msg Message) error {
		processedCount++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, processedCount)

	spans := exporter.GetSpans()
	// 1 batch span + 2 item spans
	assert.Len(t, spans, 3)
}

func TestConsume_ConvenienceFunction(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	msg := NewTestMessage("msg-conv")

	err := Consume(context.Background(), msg, func(ctx context.Context, span trace.Span) error {
		return nil
	},
		WithSystem(SystemSQS),
		WithDestination("queue"),
	)

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "aws_sqs.consume queue", spans[0].Name)
}

func TestProduce_ConvenienceFunction(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	headers := NewMapHeaderSetter()

	err := Produce(context.Background(), headers, func(ctx context.Context) error {
		return nil
	},
		WithProducerSystem(SystemRabbitMQ),
		WithProducerDestination("events"),
	)

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "rabbitmq.publish events", spans[0].Name)
}

func TestRecordDLQ(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")

	RecordDLQ(ctx, DLQInfo{
		QueueName:         "orders-dlq",
		Reason:            "max_retries",
		OriginalMessageID: "msg-123",
		RetryCount:        3,
	})

	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, "orders-dlq", attrMap[AttrDLQName])
	assert.Equal(t, "max_retries", attrMap[AttrDLQReason])
	assert.Equal(t, int64(3), attrMap[AttrRetryCount])
}

func TestRecordRetry(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")

	RecordRetry(ctx, RetryInfo{
		Count:     2,
		BackoffMs: 5000,
		Reason:    "transient_error",
	})

	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, int64(2), attrMap[AttrRetryCount])
	assert.Equal(t, int64(5000), attrMap[AttrRetryBackoffMs])
	assert.Equal(t, "transient_error", attrMap[AttrRetryReason])
}

func TestRecordConsumerLag(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")

	RecordConsumerLag(ctx, ConsumerLagInfo{
		Lag:       1000,
		Partition: 5,
	})

	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, int64(1000), attrMap[AttrKafkaConsumerLag])
	assert.Equal(t, int64(5), attrMap[AttrKafkaPartition])
}

func TestConsumerWithRetry(t *testing.T) {
	exporter, cleanup := setupTestTracer(t)
	defer cleanup()

	consumer := NewConsumer(
		WithSystem(SystemKafka),
		WithDestination("orders"),
	)

	msg := NewTestMessage("msg-retry")

	err := consumer.ConsumeWithRetry(context.Background(), msg, 3, func(ctx context.Context, span trace.Span) error {
		return nil
	})

	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, int64(3), attrMap[AttrRetryCount])
}
