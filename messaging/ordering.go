package messaging

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OrderingCheck represents the result of an ordering check.
type OrderingCheck int

const (
	// OrderingOK means the message is in sequence.
	OrderingOK OrderingCheck = iota
	// OrderingOutOfOrder means the message sequence number is not expected.
	OrderingOutOfOrder
	// OrderingDuplicate means this message was already seen.
	OrderingDuplicate
	// OrderingGap means there's a gap in the sequence (missing messages).
	OrderingGap
)

func (o OrderingCheck) String() string {
	switch o {
	case OrderingOK:
		return "ok"
	case OrderingOutOfOrder:
		return "out_of_order"
	case OrderingDuplicate:
		return "duplicate"
	case OrderingGap:
		return "gap"
	default:
		return "unknown"
	}
}

// OrderingTrackerConfig configures the ordering tracker.
type OrderingTrackerConfig struct {
	// DeduplicationWindowSize is the number of message IDs to remember for dedup.
	// Default: 1000
	DeduplicationWindowSize int

	// DeduplicationWindowDuration is how long to remember message IDs.
	// Default: 5 minutes
	DeduplicationWindowDuration time.Duration

	// TrackByPartition enables per-partition sequence tracking.
	// Default: true
	TrackByPartition bool

	// OnOutOfOrder is called when an out-of-order message is detected.
	OnOutOfOrder func(ctx context.Context, expected, actual int64)

	// OnDuplicate is called when a duplicate message is detected.
	OnDuplicate func(ctx context.Context, messageID string)

	// OnGap is called when a gap in the sequence is detected.
	OnGap func(ctx context.Context, lastSeq, currentSeq int64, gapSize int64)
}

// OrderingTrackerOption configures the ordering tracker.
type OrderingTrackerOption func(*OrderingTrackerConfig)

// WithDeduplicationWindowSize sets the deduplication window size.
func WithDeduplicationWindowSize(size int) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.DeduplicationWindowSize = size
	}
}

// WithDeduplicationWindowDuration sets the deduplication time window.
func WithDeduplicationWindowDuration(d time.Duration) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.DeduplicationWindowDuration = d
	}
}

// WithTrackByPartition enables or disables per-partition tracking.
func WithTrackByPartition(enabled bool) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.TrackByPartition = enabled
	}
}

// WithOnOutOfOrder sets the out-of-order callback.
func WithOnOutOfOrder(fn func(ctx context.Context, expected, actual int64)) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.OnOutOfOrder = fn
	}
}

// WithOnDuplicate sets the duplicate callback.
func WithOnDuplicate(fn func(ctx context.Context, messageID string)) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.OnDuplicate = fn
	}
}

// WithOnGap sets the gap callback.
func WithOnGap(fn func(ctx context.Context, lastSeq, currentSeq int64, gapSize int64)) OrderingTrackerOption {
	return func(c *OrderingTrackerConfig) {
		c.OnGap = fn
	}
}

// seenMessage tracks a seen message for deduplication.
type seenMessage struct {
	seenAt time.Time
}

// partitionTracker tracks sequence for a single partition.
type partitionTracker struct {
	lastSequence int64
	initialized  bool
}

// OrderingTracker tracks message ordering and detects duplicates.
type OrderingTracker struct {
	config     *OrderingTrackerConfig
	mu         sync.RWMutex
	seen       map[string]seenMessage       // messageID -> seen info
	seenOrder  []string                     // for FIFO eviction
	partitions map[string]*partitionTracker // partition key -> tracker

	// Statistics
	stats OrderingStats
}

// OrderingStats contains ordering statistics.
type OrderingStats struct {
	TotalMessages    int64
	OutOfOrderCount  int64
	DuplicateCount   int64
	GapCount         int64
	TotalGapMessages int64 // Total messages in gaps
}

func defaultOrderingConfig() *OrderingTrackerConfig {
	return &OrderingTrackerConfig{
		DeduplicationWindowSize:     1000,
		DeduplicationWindowDuration: 5 * time.Minute,
		TrackByPartition:            true,
	}
}

// NewOrderingTracker creates a new ordering tracker.
//
// Example:
//
//	tracker := messaging.NewOrderingTracker(
//	    messaging.WithDeduplicationWindowSize(5000),
//	    messaging.WithDeduplicationWindowDuration(10*time.Minute),
//	    messaging.WithOnDuplicate(func(ctx context.Context, msgID string) {
//	        log.Printf("Duplicate message detected: %s", msgID)
//	    }),
//	)
func NewOrderingTracker(opts ...OrderingTrackerOption) *OrderingTracker {
	cfg := defaultOrderingConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return &OrderingTracker{
		config:     cfg,
		seen:       make(map[string]seenMessage),
		seenOrder:  make([]string, 0, cfg.DeduplicationWindowSize),
		partitions: make(map[string]*partitionTracker),
	}
}

// CheckAndTrack checks message ordering and tracks it for deduplication.
// Returns the ordering check result.
//
// Example:
//
//	result := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
//	    ID:          msg.ID(),
//	    Sequence:    msg.Offset,
//	    Partition:   msg.Partition,
//	    Topic:       msg.Topic,
//	})
//
//	switch result {
//	case messaging.OrderingDuplicate:
//	    log.Printf("Skipping duplicate message: %s", msg.ID())
//	    return nil
//	case messaging.OrderingOutOfOrder:
//	    log.Printf("Out of order message, processing anyway")
//	}
func (t *OrderingTracker) CheckAndTrack(ctx context.Context, msg OrderedMessage) OrderingCheck {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.TotalMessages++
	span := trace.SpanFromContext(ctx)

	// Check for duplicate
	if t.isDuplicate(msg.ID) {
		t.stats.DuplicateCount++
		t.recordDuplicate(ctx, span, msg)
		if t.config.OnDuplicate != nil {
			t.config.OnDuplicate(ctx, msg.ID)
		}
		return OrderingDuplicate
	}

	// Track for future dedup
	t.trackMessage(msg.ID)

	// Check sequence ordering
	result := t.checkSequence(ctx, span, msg)

	return result
}

// isDuplicate checks if a message ID was already seen.
func (t *OrderingTracker) isDuplicate(messageID string) bool {
	if messageID == "" {
		return false
	}

	seen, ok := t.seen[messageID]
	if !ok {
		return false
	}

	// Check if within time window
	if t.config.DeduplicationWindowDuration > 0 {
		if time.Since(seen.seenAt) > t.config.DeduplicationWindowDuration {
			// Expired, treat as not duplicate
			delete(t.seen, messageID)
			return false
		}
	}

	return true
}

// trackMessage adds a message ID to the dedup window.
func (t *OrderingTracker) trackMessage(messageID string) {
	if messageID == "" {
		return
	}

	// Evict oldest if at capacity
	if len(t.seenOrder) >= t.config.DeduplicationWindowSize {
		oldest := t.seenOrder[0]
		t.seenOrder = t.seenOrder[1:]
		delete(t.seen, oldest)
	}

	t.seen[messageID] = seenMessage{seenAt: time.Now()}
	t.seenOrder = append(t.seenOrder, messageID)
}

// checkSequence checks if the message is in sequence order.
func (t *OrderingTracker) checkSequence(ctx context.Context, span trace.Span, msg OrderedMessage) OrderingCheck {
	if msg.Sequence < 0 {
		return OrderingOK // No sequence tracking for this message
	}

	partitionKey := msg.PartitionKey()
	tracker, ok := t.partitions[partitionKey]
	if !ok {
		tracker = &partitionTracker{}
		t.partitions[partitionKey] = tracker
	}

	if !tracker.initialized {
		tracker.lastSequence = msg.Sequence
		tracker.initialized = true
		t.recordSequenceOK(span, msg, true)
		return OrderingOK
	}

	expectedSequence := tracker.lastSequence + 1

	if msg.Sequence == expectedSequence {
		// Perfect ordering
		tracker.lastSequence = msg.Sequence
		t.recordSequenceOK(span, msg, false)
		return OrderingOK
	}

	if msg.Sequence <= tracker.lastSequence {
		// Out of order (already seen this sequence or earlier)
		t.stats.OutOfOrderCount++
		t.recordOutOfOrder(ctx, span, msg, expectedSequence)
		if t.config.OnOutOfOrder != nil {
			t.config.OnOutOfOrder(ctx, expectedSequence, msg.Sequence)
		}
		return OrderingOutOfOrder
	}

	// Gap detected (msg.Sequence > expectedSequence)
	gapSize := msg.Sequence - expectedSequence
	t.stats.GapCount++
	t.stats.TotalGapMessages += gapSize
	t.recordGap(ctx, span, msg, tracker.lastSequence, gapSize)
	if t.config.OnGap != nil {
		t.config.OnGap(ctx, tracker.lastSequence, msg.Sequence, gapSize)
	}

	// Update to new sequence
	tracker.lastSequence = msg.Sequence
	return OrderingGap
}

// recordDuplicate records duplicate message detection.
func (t *OrderingTracker) recordDuplicate(ctx context.Context, span trace.Span, msg OrderedMessage) {
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Bool("messaging.ordering.duplicate", true),
			attribute.String("messaging.ordering.duplicate.message_id", msg.ID),
		)

		span.AddEvent("message.duplicate_detected", trace.WithAttributes(
			attribute.String("messaging.ordering.duplicate.message_id", msg.ID),
			attribute.Int64("messaging.ordering.stats.duplicate_count", t.stats.DuplicateCount),
		))
	}
}

// recordSequenceOK records successful sequence check.
func (t *OrderingTracker) recordSequenceOK(span trace.Span, msg OrderedMessage, firstMessage bool) {
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Bool("messaging.ordering.in_sequence", true),
			attribute.Int64("messaging.ordering.sequence", msg.Sequence),
		)

		if firstMessage {
			span.AddEvent("message.sequence_initialized", trace.WithAttributes(
				attribute.Int64("messaging.ordering.sequence", msg.Sequence),
				attribute.String("messaging.ordering.partition_key", msg.PartitionKey()),
			))
		}
	}
}

// recordOutOfOrder records out-of-order message detection.
func (t *OrderingTracker) recordOutOfOrder(ctx context.Context, span trace.Span, msg OrderedMessage, expected int64) {
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Bool("messaging.ordering.out_of_order", true),
			attribute.Int64("messaging.ordering.sequence.expected", expected),
			attribute.Int64("messaging.ordering.sequence.actual", msg.Sequence),
		)

		span.AddEvent("message.out_of_order", trace.WithAttributes(
			attribute.Int64("messaging.ordering.sequence.expected", expected),
			attribute.Int64("messaging.ordering.sequence.actual", msg.Sequence),
			attribute.String("messaging.ordering.partition_key", msg.PartitionKey()),
			attribute.Int64("messaging.ordering.stats.out_of_order_count", t.stats.OutOfOrderCount),
		))
	}
}

// recordGap records sequence gap detection.
func (t *OrderingTracker) recordGap(ctx context.Context, span trace.Span, msg OrderedMessage, lastSeq, gapSize int64) {
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Bool("messaging.ordering.gap_detected", true),
			attribute.Int64("messaging.ordering.gap.last_sequence", lastSeq),
			attribute.Int64("messaging.ordering.gap.current_sequence", msg.Sequence),
			attribute.Int64("messaging.ordering.gap.size", gapSize),
		)

		span.AddEvent("message.sequence_gap", trace.WithAttributes(
			attribute.Int64("messaging.ordering.gap.last_sequence", lastSeq),
			attribute.Int64("messaging.ordering.gap.current_sequence", msg.Sequence),
			attribute.Int64("messaging.ordering.gap.size", gapSize),
			attribute.String("messaging.ordering.partition_key", msg.PartitionKey()),
			attribute.Int64("messaging.ordering.stats.gap_count", t.stats.GapCount),
			attribute.Int64("messaging.ordering.stats.total_gap_messages", t.stats.TotalGapMessages),
		))
	}
}

// Stats returns the current ordering statistics.
func (t *OrderingTracker) Stats() OrderingStats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stats
}

// Reset clears all tracked state.
func (t *OrderingTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.seen = make(map[string]seenMessage)
	t.seenOrder = make([]string, 0, t.config.DeduplicationWindowSize)
	t.partitions = make(map[string]*partitionTracker)
	t.stats = OrderingStats{}
}

// OrderedMessage represents a message with ordering information.
type OrderedMessage struct {
	ID        string // Message ID for deduplication
	Sequence  int64  // Sequence number (offset for Kafka)
	Partition int    // Partition number (-1 if not applicable)
	Topic     string // Topic name (optional)
}

// PartitionKey returns a unique key for the partition.
func (m OrderedMessage) PartitionKey() string {
	if m.Topic == "" {
		return fmt.Sprintf("partition:%d", m.Partition)
	}
	return fmt.Sprintf("%s:%d", m.Topic, m.Partition)
}

// RecordOrderingResult records an ordering check result on the current span.
// Use this for manual ordering tracking without the full tracker.
func RecordOrderingResult(ctx context.Context, result OrderingCheck, msgID string, sequence int64) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.SetAttributes(
		attribute.String("messaging.ordering.result", result.String()),
	)

	if msgID != "" {
		span.SetAttributes(attribute.String("messaging.ordering.message_id", msgID))
	}

	if sequence >= 0 {
		span.SetAttributes(attribute.Int64("messaging.ordering.sequence", sequence))
	}

	switch result {
	case OrderingDuplicate:
		span.SetAttributes(attribute.Bool("messaging.ordering.duplicate", true))
	case OrderingOutOfOrder:
		span.SetAttributes(attribute.Bool("messaging.ordering.out_of_order", true))
	case OrderingGap:
		span.SetAttributes(attribute.Bool("messaging.ordering.gap_detected", true))
	}
}

// CheckDuplicate is a simple deduplication check using a provided set.
// This is a convenience function for basic deduplication without the full tracker.
//
// Example:
//
//	seenIDs := make(map[string]bool)
//	if messaging.CheckDuplicate(ctx, seenIDs, msg.ID()) {
//	    return nil // Skip duplicate
//	}
func CheckDuplicate(ctx context.Context, seen map[string]bool, messageID string) bool {
	if messageID == "" {
		return false
	}

	if seen[messageID] {
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			span.SetAttributes(attribute.Bool("messaging.ordering.duplicate", true))
			span.AddEvent("message.duplicate_detected", trace.WithAttributes(
				attribute.String("messaging.ordering.duplicate.message_id", messageID),
			))
		}
		return true
	}

	seen[messageID] = true
	return false
}
