package messaging

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/sampling"
)

// BatchConfig holds configuration for batch processing.
type BatchConfig struct {
	System          string
	Destination     string
	SpanNameFunc    func(batchSize int) string
	ExtraAttributes func(batchSize int) []attribute.KeyValue
	SampleIDs       int  // Number of message IDs to include in span (for debugging)
	CreateLinks     bool // Create links to producer spans
	TracerName      string
}

// BatchOption configures batch processing.
type BatchOption func(*BatchConfig)

// WithBatchSystem sets the messaging system for batch processing.
func WithBatchSystem(system string) BatchOption {
	return func(c *BatchConfig) {
		c.System = system
	}
}

// WithBatchDestination sets the topic/queue name for batch processing.
func WithBatchDestination(destination string) BatchOption {
	return func(c *BatchConfig) {
		c.Destination = destination
	}
}

// WithBatchSpanName sets a custom span name function.
func WithBatchSpanName(fn func(batchSize int) string) BatchOption {
	return func(c *BatchConfig) {
		c.SpanNameFunc = fn
	}
}

// WithBatchAttributes adds custom attributes to batch spans.
func WithBatchAttributes(fn func(batchSize int) []attribute.KeyValue) BatchOption {
	return func(c *BatchConfig) {
		c.ExtraAttributes = fn
	}
}

// WithSampleIDs sets the number of message IDs to include in the span.
func WithSampleIDs(count int) BatchOption {
	return func(c *BatchConfig) {
		c.SampleIDs = count
	}
}

// WithBatchLinks enables creating links from all messages in the batch.
func WithBatchLinks() BatchOption {
	return func(c *BatchConfig) {
		c.CreateLinks = true
	}
}

// WithBatchTracerName sets a custom tracer name.
func WithBatchTracerName(name string) BatchOption {
	return func(c *BatchConfig) {
		c.TracerName = name
	}
}

func defaultBatchConfig() *BatchConfig {
	return &BatchConfig{
		System:      "unknown",
		Destination: "unknown",
		SpanNameFunc: func(size int) string {
			return fmt.Sprintf("process_batch (%d)", size)
		},
		SampleIDs:   5,
		CreateLinks: true,
		TracerName:  "autotel/messaging",
	}
}

// BatchProcessor handles batch message processing with tracing.
type BatchProcessor struct {
	config *BatchConfig
	tracer trace.Tracer
}

// NewBatchProcessor creates a new batch processor.
//
// Example:
//
//	processor := messaging.NewBatchProcessor(
//	    messaging.WithBatchSystem(messaging.SystemKafka),
//	    messaging.WithBatchDestination("orders"),
//	    messaging.WithSampleIDs(3),
//	)
//
//	err := processor.Process(ctx, messages, func(ctx context.Context, span trace.Span) (int, error) {
//	    failedCount := 0
//	    for _, msg := range messages {
//	        if err := process(msg); err != nil {
//	            failedCount++
//	        }
//	    }
//	    return failedCount, nil
//	})
func NewBatchProcessor(opts ...BatchOption) *BatchProcessor {
	cfg := defaultBatchConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return &BatchProcessor{
		config: cfg,
		tracer: otel.Tracer(cfg.TracerName),
	}
}

// Process handles a batch of messages with a single span.
// The handler returns the number of failed messages.
func (p *BatchProcessor) Process(ctx context.Context, messages []Message, handler func(context.Context, trace.Span) (failedCount int, err error)) error {
	batchSize := len(messages)
	if batchSize == 0 {
		return nil
	}

	spanName := fmt.Sprintf("%s.consume_batch %s", p.config.System, p.config.Destination)
	if p.config.SpanNameFunc != nil {
		spanName = p.config.SpanNameFunc(batchSize)
	}

	// Build attributes
	attrs := []attribute.KeyValue{
		System(p.config.System),
		Destination(p.config.Destination),
		Operation(OperationProcess),
		BatchSize(batchSize),
	}

	// Sample message IDs for debugging
	if p.config.SampleIDs > 0 {
		sampleCount := p.config.SampleIDs
		if sampleCount > batchSize {
			sampleCount = batchSize
		}
		ids := make([]string, sampleCount)
		for i := 0; i < sampleCount; i++ {
			ids[i] = messages[i].ID()
		}
		attrs = append(attrs, attribute.StringSlice(AttrBatchMessageIDs, ids))
	}

	if p.config.ExtraAttributes != nil {
		attrs = append(attrs, p.config.ExtraAttributes(batchSize)...)
	}

	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attrs...),
	}

	// Create links from all messages in batch (fan-in)
	if p.config.CreateLinks {
		links := sampling.ExtractLinksFromBatch(messages, func(m Message) map[string]string {
			return m.Headers()
		})
		if len(links) > 0 {
			spanOpts = append(spanOpts, trace.WithLinks(links...))
		}
	}

	ctx, span := p.tracer.Start(ctx, spanName, spanOpts...)
	defer span.End()

	span.AddEvent("batch.processing_started", trace.WithAttributes(BatchSize(batchSize)))

	failedCount, err := handler(ctx, span)

	// Record results
	span.SetAttributes(BatchFailedCount(failedCount))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if failedCount > 0 {
		span.SetStatus(codes.Error, fmt.Sprintf("%d messages failed", failedCount))
	}

	span.AddEvent("batch.processing_completed", trace.WithAttributes(
		BatchSize(batchSize),
		BatchFailedCount(failedCount),
	))

	return err
}

// ProcessBatch is a convenience function for one-off batch processing.
func ProcessBatch(ctx context.Context, messages []Message, handler func(context.Context, trace.Span) (failedCount int, err error), opts ...BatchOption) error {
	processor := NewBatchProcessor(opts...)
	return processor.Process(ctx, messages, handler)
}

// ProcessEach processes each message in a batch individually, creating per-item spans.
// Use this when you need detailed per-message tracing within a batch.
//
// This creates:
// - One parent "batch" span
// - One child span per message (linked to producers if configured)
func (p *BatchProcessor) ProcessEach(ctx context.Context, messages []Message, handler func(context.Context, trace.Span, Message) error) error {
	batchSize := len(messages)
	if batchSize == 0 {
		return nil
	}

	spanName := fmt.Sprintf("%s.consume_batch %s", p.config.System, p.config.Destination)
	if p.config.SpanNameFunc != nil {
		spanName = p.config.SpanNameFunc(batchSize)
	}

	attrs := []attribute.KeyValue{
		System(p.config.System),
		Destination(p.config.Destination),
		Operation(OperationProcess),
		BatchSize(batchSize),
	}

	ctx, batchSpan := p.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attrs...),
	)
	defer batchSpan.End()

	batchSpan.AddEvent("batch.processing_started")

	failedCount := 0
	for i, msg := range messages {
		itemSpanName := fmt.Sprintf("%s.process %s [%d/%d]", p.config.System, p.config.Destination, i+1, batchSize)

		itemOpts := []trace.SpanStartOption{
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				MessageID(msg.ID()),
				attribute.Int("batch.index", i),
			),
		}

		// Link to producer if configured
		if p.config.CreateLinks {
			if link, ok := sampling.CreateLinkFromHeaders(msg.Headers()); ok {
				itemOpts = append(itemOpts, trace.WithLinks(link))
			}
		}

		itemCtx, itemSpan := p.tracer.Start(ctx, itemSpanName, itemOpts...)

		err := handler(itemCtx, itemSpan, msg)
		if err != nil {
			failedCount++
			itemSpan.RecordError(err)
			itemSpan.SetStatus(codes.Error, err.Error())
		}

		itemSpan.End()
	}

	batchSpan.SetAttributes(BatchFailedCount(failedCount))
	batchSpan.AddEvent("batch.processing_completed", trace.WithAttributes(
		BatchSize(batchSize),
		BatchFailedCount(failedCount),
	))

	if failedCount > 0 {
		batchSpan.SetStatus(codes.Error, fmt.Sprintf("%d/%d messages failed", failedCount, batchSize))
	}

	return nil
}
