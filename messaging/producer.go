package messaging

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// HeaderSetter allows setting headers on outgoing messages.
type HeaderSetter interface {
	SetHeader(key, value string)
}

// ProducerConfig holds configuration for producer tracing.
type ProducerConfig struct {
	System          string
	Destination     string
	Propagator      propagation.TextMapPropagator
	SpanNameFunc    func(destination string) string
	ExtraAttributes func() []attribute.KeyValue
	TracerName      string
}

// ProducerOption configures producer tracing.
type ProducerOption func(*ProducerConfig)

// WithProducerSystem sets the messaging system for the producer.
func WithProducerSystem(system string) ProducerOption {
	return func(c *ProducerConfig) {
		c.System = system
	}
}

// WithProducerDestination sets the topic/queue name for the producer.
func WithProducerDestination(destination string) ProducerOption {
	return func(c *ProducerConfig) {
		c.Destination = destination
	}
}

// WithProducerPropagator sets a custom propagator for context injection.
func WithProducerPropagator(propagator propagation.TextMapPropagator) ProducerOption {
	return func(c *ProducerConfig) {
		c.Propagator = propagator
	}
}

// WithProducerSpanName sets a custom span name function.
func WithProducerSpanName(fn func(destination string) string) ProducerOption {
	return func(c *ProducerConfig) {
		c.SpanNameFunc = fn
	}
}

// WithProducerAttributes adds custom attributes to producer spans.
func WithProducerAttributes(fn func() []attribute.KeyValue) ProducerOption {
	return func(c *ProducerConfig) {
		c.ExtraAttributes = fn
	}
}

// WithProducerTracerName sets a custom tracer name for the producer.
func WithProducerTracerName(name string) ProducerOption {
	return func(c *ProducerConfig) {
		c.TracerName = name
	}
}

func defaultProducerConfig() *ProducerConfig {
	return &ProducerConfig{
		System:      "unknown",
		Destination: "unknown",
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		SpanNameFunc: nil, // nil = use default "system.publish destination" naming
		TracerName:   "autotel/messaging",
	}
}

// Producer wraps message publishing with tracing.
type Producer struct {
	config *ProducerConfig
	tracer trace.Tracer
}

// NewProducer creates a new traced producer.
//
// Example:
//
//	producer := messaging.NewProducer(
//	    messaging.WithProducerSystem(messaging.SystemKafka),
//	    messaging.WithProducerDestination("orders"),
//	)
//
//	err := producer.Publish(ctx, &msg, func(ctx context.Context) error {
//	    return kafkaProducer.Send(msg)
//	})
func NewProducer(opts ...ProducerOption) *Producer {
	cfg := defaultProducerConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return &Producer{
		config: cfg,
		tracer: otel.Tracer(cfg.TracerName),
	}
}

// Publish sends a message with tracing.
// It creates a PRODUCER span, injects trace context into message headers,
// and executes the send function.
//
// The headerSetter should set headers on the message being sent.
// If headerSetter is nil, context is not propagated (useful for fire-and-forget).
func (p *Producer) Publish(ctx context.Context, headerSetter HeaderSetter, send func(context.Context) error) error {
	spanName := fmt.Sprintf("%s.publish %s", p.config.System, p.config.Destination)
	if p.config.SpanNameFunc != nil {
		spanName = p.config.SpanNameFunc(p.config.Destination)
	}

	// Build attributes
	attrs := []attribute.KeyValue{
		System(p.config.System),
		Destination(p.config.Destination),
		Operation(OperationPublish),
	}

	if p.config.ExtraAttributes != nil {
		attrs = append(attrs, p.config.ExtraAttributes()...)
	}

	ctx, span := p.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attrs...),
	)
	defer span.End()

	// Inject trace context into message headers
	if headerSetter != nil {
		p.config.Propagator.Inject(ctx, &headerCarrier{setter: headerSetter})
	}

	span.AddEvent("message.publish")

	err := send(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// PublishWithID is like Publish but also records the message ID.
func (p *Producer) PublishWithID(ctx context.Context, msgID string, headerSetter HeaderSetter, send func(context.Context) error) error {
	spanName := fmt.Sprintf("%s.publish %s", p.config.System, p.config.Destination)
	if p.config.SpanNameFunc != nil {
		spanName = p.config.SpanNameFunc(p.config.Destination)
	}

	attrs := []attribute.KeyValue{
		System(p.config.System),
		Destination(p.config.Destination),
		Operation(OperationPublish),
		MessageID(msgID),
	}

	if p.config.ExtraAttributes != nil {
		attrs = append(attrs, p.config.ExtraAttributes()...)
	}

	ctx, span := p.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attrs...),
	)
	defer span.End()

	if headerSetter != nil {
		p.config.Propagator.Inject(ctx, &headerCarrier{setter: headerSetter})
	}

	span.AddEvent("message.publish", trace.WithAttributes(MessageID(msgID)))

	err := send(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Produce is a convenience function for one-off message publishing.
//
// Example:
//
//	err := messaging.Produce(ctx, &msg, func(ctx context.Context) error {
//	    return kafkaProducer.Send(msg)
//	},
//	    messaging.WithProducerSystem(messaging.SystemKafka),
//	    messaging.WithProducerDestination("orders"),
//	)
func Produce(ctx context.Context, headerSetter HeaderSetter, send func(context.Context) error, opts ...ProducerOption) error {
	producer := NewProducer(opts...)
	return producer.Publish(ctx, headerSetter, send)
}

// headerCarrier adapts HeaderSetter to propagation.TextMapCarrier
type headerCarrier struct {
	setter HeaderSetter
}

func (c *headerCarrier) Get(key string) string {
	return "" // Not used for injection
}

func (c *headerCarrier) Set(key, value string) {
	c.setter.SetHeader(key, value)
}

func (c *headerCarrier) Keys() []string {
	return nil // Not used for injection
}

// MapHeaderSetter is a simple HeaderSetter implementation using a map.
type MapHeaderSetter map[string]string

func (m MapHeaderSetter) SetHeader(key, value string) {
	m[key] = value
}

// NewMapHeaderSetter creates a new map-based header setter.
func NewMapHeaderSetter() MapHeaderSetter {
	return make(MapHeaderSetter)
}
