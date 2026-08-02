package messaging

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/sampling"
)

// Message represents a message from any messaging system.
// Implement this interface to use the consumer middleware with your message type.
type Message interface {
	// ID returns a unique identifier for the message
	ID() string
	// Headers returns the message headers for trace context extraction
	Headers() map[string]string
	// Payload returns the message payload (for size calculation)
	Payload() []byte
}

// ConsumerConfig holds configuration for consumer tracing.
type ConsumerConfig struct {
	System          string
	Destination     string
	ConsumerGroup   string
	Propagator      propagation.TextMapPropagator
	SpanNameFunc    func(msg Message) string
	ExtraAttributes func(msg Message) []attribute.KeyValue
	OnError         func(ctx context.Context, msg Message, err error)
	CreateLinks     bool // If true, create links to producer spans instead of parent-child
	RecordPayload   bool // If true, record payload size
	TracerName      string
}

// ConsumerOption configures consumer tracing.
type ConsumerOption func(*ConsumerConfig)

// WithSystem sets the messaging system (kafka, rabbitmq, sqs, etc.)
func WithSystem(system string) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.System = system
	}
}

// WithDestination sets the topic/queue name.
func WithDestination(destination string) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.Destination = destination
	}
}

// WithConsumerGroup sets the consumer group (for Kafka).
func WithConsumerGroup(group string) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.ConsumerGroup = group
	}
}

// WithPropagator sets a custom propagator for context extraction.
func WithPropagator(propagator propagation.TextMapPropagator) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.Propagator = propagator
	}
}

// WithSpanName sets a custom span name function.
func WithSpanName(fn func(msg Message) string) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.SpanNameFunc = fn
	}
}

// WithExtraAttributes adds custom attributes to the span.
func WithExtraAttributes(fn func(msg Message) []attribute.KeyValue) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.ExtraAttributes = fn
	}
}

// OnError sets an error callback (e.g., for DLQ handling).
func OnError(fn func(ctx context.Context, msg Message, err error)) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.OnError = fn
	}
}

// WithLinks enables link-based tracing instead of parent-child.
// Use this for event-driven architectures where consumer traces should be
// independent but linked to producer traces.
func WithLinks() ConsumerOption {
	return func(c *ConsumerConfig) {
		c.CreateLinks = true
	}
}

// WithPayloadRecording enables recording payload size.
func WithPayloadRecording() ConsumerOption {
	return func(c *ConsumerConfig) {
		c.RecordPayload = true
	}
}

// WithTracerName sets a custom tracer name.
func WithTracerName(name string) ConsumerOption {
	return func(c *ConsumerConfig) {
		c.TracerName = name
	}
}

func defaultConsumerConfig() *ConsumerConfig {
	return &ConsumerConfig{
		System:      "unknown",
		Destination: "unknown",
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		SpanNameFunc: nil,  // nil = use default "system.consume destination" naming
		CreateLinks:  true, // Default to links for event-driven
		TracerName:   "autotel/messaging",
	}
}

// Consumer wraps a message handler with tracing.
// It extracts trace context from message headers and creates a properly
// attributed span following OTel messaging conventions.
type Consumer struct {
	config *ConsumerConfig
	tracer trace.Tracer
}

// NewConsumer creates a new traced consumer.
//
// Example:
//
//	consumer := messaging.NewConsumer(
//	    messaging.WithSystem(messaging.SystemKafka),
//	    messaging.WithDestination("orders"),
//	    messaging.WithConsumerGroup("order-processor"),
//	    messaging.WithLinks(), // Use links instead of parent-child
//	)
//
//	for msg := range messages {
//	    err := consumer.Process(ctx, msg, func(ctx context.Context, span trace.Span) error {
//	        return processOrder(ctx, msg)
//	    })
//	}
func NewConsumer(opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return &Consumer{
		config: cfg,
		tracer: otel.Tracer(cfg.TracerName),
	}
}

// Process handles a message with full tracing.
// The handler receives a context with trace context and a span for adding
// custom attributes/events.
func (c *Consumer) Process(ctx context.Context, msg Message, handler func(context.Context, trace.Span) error) error {
	// Build span name: "system.consume destination" (e.g., "kafka.consume orders")
	spanName := fmt.Sprintf("%s.consume %s", c.config.System, c.config.Destination)
	if c.config.SpanNameFunc != nil {
		spanName = c.config.SpanNameFunc(msg)
	}

	// Extract trace context from message headers
	headers := msg.Headers()
	carrier := propagation.MapCarrier(headers)

	var span trace.Span
	var spanOpts []trace.SpanStartOption

	// Always set span kind to CONSUMER
	spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindConsumer))

	// Build attributes
	attrs := []attribute.KeyValue{
		System(c.config.System),
		Destination(c.config.Destination),
		Operation(OperationProcess),
		MessageID(msg.ID()),
	}

	if c.config.ConsumerGroup != "" {
		attrs = append(attrs, KafkaConsumerGroup(c.config.ConsumerGroup))
	}

	if c.config.RecordPayload && msg.Payload() != nil {
		attrs = append(attrs, PayloadSize(len(msg.Payload())))
	}

	// Add extra attributes if configured
	if c.config.ExtraAttributes != nil {
		attrs = append(attrs, c.config.ExtraAttributes(msg)...)
	}

	spanOpts = append(spanOpts, trace.WithAttributes(attrs...))

	if c.config.CreateLinks {
		// Links mode: Create a new trace with a link to the producer
		link, ok := sampling.CreateLinkFromHeaders(headers)
		if ok {
			spanOpts = append(spanOpts, trace.WithLinks(link))
		}
		ctx, span = c.tracer.Start(ctx, spanName, spanOpts...)
	} else {
		// Parent-child mode: Extract context and continue the trace
		extractedCtx := c.config.Propagator.Extract(ctx, carrier)
		ctx, span = c.tracer.Start(extractedCtx, spanName, spanOpts...)
	}
	defer span.End()

	// Add receive event
	span.AddEvent("message.received")

	// Execute handler
	err := handler(ctx, span)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		// Call error callback if configured
		if c.config.OnError != nil {
			c.config.OnError(ctx, msg, err)
		}
	}

	return err
}

// Consume is a convenience function for one-off message processing.
//
// Example:
//
//	err := messaging.Consume(ctx, msg, func(ctx context.Context, span trace.Span) error {
//	    return processOrder(ctx, msg)
//	},
//	    messaging.WithSystem(messaging.SystemKafka),
//	    messaging.WithDestination("orders"),
//	)
func Consume(ctx context.Context, msg Message, handler func(context.Context, trace.Span) error, opts ...ConsumerOption) error {
	consumer := NewConsumer(opts...)
	return consumer.Process(ctx, msg, handler)
}

// ConsumeWithRetry processes a message with retry tracking.
// It automatically adds retry attributes to the span.
func (c *Consumer) ConsumeWithRetry(ctx context.Context, msg Message, retryCount int, handler func(context.Context, trace.Span) error) error {
	return c.Process(ctx, msg, func(ctx context.Context, span trace.Span) error {
		if retryCount > 0 {
			span.SetAttributes(RetryCount(retryCount))
			span.AddEvent("message.retry", trace.WithAttributes(
				attribute.Int("retry.attempt", retryCount),
			))
		}
		return handler(ctx, span)
	})
}
