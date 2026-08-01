package messaging

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RebalanceType represents the type of consumer group rebalance event.
type RebalanceType string

const (
	RebalanceAssigned RebalanceType = "assigned"
	RebalanceRevoked  RebalanceType = "revoked"
	RebalanceLost     RebalanceType = "lost"
)

// PartitionAssignment represents a partition assignment.
type PartitionAssignment struct {
	Topic     string
	Partition int
	Offset    int64 // -1 if not available
}

// RebalanceEvent contains information about a consumer group rebalance.
type RebalanceEvent struct {
	Type       RebalanceType
	Partitions []PartitionAssignment
	Timestamp  time.Time
	Generation int    // Group generation ID (-1 if not available)
	MemberID   string // Consumer member ID
	Reason     string // Optional reason for rebalance
}

// PartitionLagInfo contains partition lag metrics.
type PartitionLagInfo struct {
	Topic         string
	Partition     int
	CurrentOffset int64
	EndOffset     int64 // High watermark
	Lag           int64
	Timestamp     time.Time
}

// ConsumerGroupState represents a snapshot of consumer group state.
type ConsumerGroupState struct {
	GroupID            string
	MemberID           string
	GroupInstanceID    string // Static membership instance ID
	AssignedPartitions []PartitionAssignment
	Generation         int
	IsActive           bool
	LastHeartbeat      time.Time
	State              string // stable, preparing_rebalance, completing_rebalance, dead, empty
}

// ConsumerGroupTracker tracks consumer group state and records events.
type ConsumerGroupTracker struct {
	state                *ConsumerGroupState
	onRebalance          func(event RebalanceEvent)
	onPartitionsAssigned func(partitions []PartitionAssignment)
	onPartitionsRevoked  func(partitions []PartitionAssignment)
}

// ConsumerGroupTrackerOption configures the tracker.
type ConsumerGroupTrackerOption func(*ConsumerGroupTracker)

// WithConsumerGroupID sets the consumer group ID.
func WithConsumerGroupID(groupID string) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.state.GroupID = groupID
	}
}

// WithMemberID sets the consumer member ID.
func WithMemberID(memberID string) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.state.MemberID = memberID
	}
}

// WithGroupInstanceID sets the static group instance ID.
func WithGroupInstanceID(instanceID string) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.state.GroupInstanceID = instanceID
	}
}

// WithOnRebalance sets a callback for rebalance events.
func WithOnRebalance(fn func(event RebalanceEvent)) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.onRebalance = fn
	}
}

// WithOnPartitionsAssigned sets a callback for partition assignments.
func WithOnPartitionsAssigned(fn func(partitions []PartitionAssignment)) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.onPartitionsAssigned = fn
	}
}

// WithOnPartitionsRevoked sets a callback for partition revocations.
func WithOnPartitionsRevoked(fn func(partitions []PartitionAssignment)) ConsumerGroupTrackerOption {
	return func(t *ConsumerGroupTracker) {
		t.onPartitionsRevoked = fn
	}
}

// NewConsumerGroupTracker creates a new consumer group tracker.
func NewConsumerGroupTracker(opts ...ConsumerGroupTrackerOption) *ConsumerGroupTracker {
	t := &ConsumerGroupTracker{
		state: &ConsumerGroupState{
			IsActive:   true,
			Generation: -1,
		},
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// State returns a copy of the current consumer group state.
func (t *ConsumerGroupTracker) State() ConsumerGroupState {
	return ConsumerGroupState{
		GroupID:            t.state.GroupID,
		MemberID:           t.state.MemberID,
		GroupInstanceID:    t.state.GroupInstanceID,
		AssignedPartitions: append([]PartitionAssignment{}, t.state.AssignedPartitions...),
		Generation:         t.state.Generation,
		IsActive:           t.state.IsActive,
		LastHeartbeat:      t.state.LastHeartbeat,
		State:              t.state.State,
	}
}

// RecordRebalance records a rebalance event on the span and updates internal state.
//
// Example:
//
//	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
//	    Type:       messaging.RebalanceAssigned,
//	    Partitions: []messaging.PartitionAssignment{{Topic: "orders", Partition: 0}},
//	    Timestamp:  time.Now(),
//	    Generation: 5,
//	    MemberID:   "consumer-1",
//	})
func (t *ConsumerGroupTracker) RecordRebalance(ctx context.Context, event RebalanceEvent) {
	// Update internal state
	t.updateState(event)

	// Record to span
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		t.recordRebalanceToSpan(span, event)
	}

	// Call user callbacks
	t.invokeCallbacks(event)
}

// updateState updates internal tracker state based on rebalance event.
func (t *ConsumerGroupTracker) updateState(event RebalanceEvent) {
	switch event.Type {
	case RebalanceAssigned:
		t.state.AssignedPartitions = event.Partitions
		t.state.IsActive = true
		t.state.State = "stable"
	case RebalanceRevoked:
		t.state.AssignedPartitions = t.removePartitions(event.Partitions)
		if len(t.state.AssignedPartitions) == 0 {
			t.state.State = "empty"
		} else {
			t.state.State = "preparing_rebalance"
		}
	case RebalanceLost:
		t.state.AssignedPartitions = t.removePartitions(event.Partitions)
		t.state.IsActive = false
		t.state.State = "dead"
	}

	if event.Generation >= 0 {
		t.state.Generation = event.Generation
	}
	if event.MemberID != "" {
		t.state.MemberID = event.MemberID
	}
}

// removePartitions removes the given partitions from the assigned set.
func (t *ConsumerGroupTracker) removePartitions(toRemove []PartitionAssignment) []PartitionAssignment {
	removeSet := make(map[string]bool)
	for _, p := range toRemove {
		removeSet[partitionKey(p.Topic, p.Partition)] = true
	}

	remaining := make([]PartitionAssignment, 0)
	for _, p := range t.state.AssignedPartitions {
		if !removeSet[partitionKey(p.Topic, p.Partition)] {
			remaining = append(remaining, p)
		}
	}
	return remaining
}

// recordRebalanceToSpan records rebalance event attributes and events to the span.
func (t *ConsumerGroupTracker) recordRebalanceToSpan(span trace.Span, event RebalanceEvent) {
	// Base attributes
	span.SetAttributes(
		attribute.String("messaging.consumer_group.rebalance.type", string(event.Type)),
		attribute.Int("messaging.consumer_group.rebalance.partition_count", len(event.Partitions)),
	)

	// Optional attributes
	if event.Generation >= 0 {
		span.SetAttributes(attribute.Int("messaging.consumer_group.generation", event.Generation))
	}
	if event.MemberID != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.member_id", event.MemberID))
	}
	if event.Reason != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.rebalance.reason", event.Reason))
	}
	if t.state.State != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.state", t.state.State))
	}

	// Partition details (if small enough)
	if len(event.Partitions) <= 10 {
		partitions := make([]string, len(event.Partitions))
		for i, p := range event.Partitions {
			partitions[i] = partitionKey(p.Topic, p.Partition)
		}
		span.SetAttributes(attribute.StringSlice("messaging.consumer_group.rebalance.partitions", partitions))
	}

	// Record event
	eventAttrs := []attribute.KeyValue{
		attribute.String("messaging.consumer_group.rebalance.type", string(event.Type)),
		attribute.Int("messaging.consumer_group.rebalance.partition_count", len(event.Partitions)),
		attribute.Int64("messaging.consumer_group.rebalance.timestamp", event.Timestamp.UnixMilli()),
	}
	if event.Generation >= 0 {
		eventAttrs = append(eventAttrs, attribute.Int("messaging.consumer_group.generation", event.Generation))
	}
	span.AddEvent("consumer_group_"+string(event.Type), trace.WithAttributes(eventAttrs...))
}

// invokeCallbacks calls user-registered callbacks for the event.
func (t *ConsumerGroupTracker) invokeCallbacks(event RebalanceEvent) {
	if t.onRebalance != nil {
		t.onRebalance(event)
	}
	if event.Type == RebalanceAssigned && t.onPartitionsAssigned != nil {
		t.onPartitionsAssigned(event.Partitions)
	}
	if event.Type == RebalanceRevoked && t.onPartitionsRevoked != nil {
		t.onPartitionsRevoked(event.Partitions)
	}
}

// RecordHeartbeat records a heartbeat event.
//
// Example:
//
//	tracker.RecordHeartbeat(ctx, true, 5*time.Millisecond)
func (t *ConsumerGroupTracker) RecordHeartbeat(ctx context.Context, healthy bool, latency time.Duration) {
	span := trace.SpanFromContext(ctx)
	t.state.LastHeartbeat = time.Now()

	if span.IsRecording() {
		span.SetAttributes(attribute.Bool("messaging.consumer_group.heartbeat.healthy", healthy))

		if latency > 0 {
			span.SetAttributes(attribute.Int64("messaging.consumer_group.heartbeat.latency_ms", latency.Milliseconds()))
		}

		eventAttrs := []attribute.KeyValue{
			attribute.Bool("messaging.consumer_group.heartbeat.healthy", healthy),
			attribute.Int64("messaging.consumer_group.heartbeat.timestamp", t.state.LastHeartbeat.UnixMilli()),
		}
		if latency > 0 {
			eventAttrs = append(eventAttrs, attribute.Int64("messaging.consumer_group.heartbeat.latency_ms", latency.Milliseconds()))
		}

		span.AddEvent("consumer_group_heartbeat", trace.WithAttributes(eventAttrs...))
	}
}

// RecordPartitionLag records partition lag metrics.
//
// Example:
//
//	tracker.RecordPartitionLag(ctx, messaging.PartitionLagInfo{
//	    Topic:         "orders",
//	    Partition:     0,
//	    CurrentOffset: 1000,
//	    EndOffset:     1050,
//	    Lag:           50,
//	    Timestamp:     time.Now(),
//	})
func (t *ConsumerGroupTracker) RecordPartitionLag(ctx context.Context, lag PartitionLagInfo) {
	span := trace.SpanFromContext(ctx)

	if span.IsRecording() {
		prefix := "messaging.consumer_group.lag." + lag.Topic + "." + string(rune('0'+lag.Partition))

		span.SetAttributes(
			attribute.Int64(prefix+".current_offset", lag.CurrentOffset),
			attribute.Int64(prefix+".end_offset", lag.EndOffset),
			attribute.Int64(prefix+".lag", lag.Lag),
		)

		span.AddEvent("partition_lag_recorded", trace.WithAttributes(
			attribute.String("messaging.consumer_group.lag.topic", lag.Topic),
			attribute.Int("messaging.consumer_group.lag.partition", lag.Partition),
			attribute.Int64("messaging.consumer_group.lag.current_offset", lag.CurrentOffset),
			attribute.Int64("messaging.consumer_group.lag.end_offset", lag.EndOffset),
			attribute.Int64("messaging.consumer_group.lag.lag", lag.Lag),
			attribute.Int64("messaging.consumer_group.lag.timestamp", lag.Timestamp.UnixMilli()),
		))
	}
}

// partitionKey creates a unique key for topic:partition
func partitionKey(topic string, partition int) string {
	return topic + ":" + string(rune('0'+partition))
}

// RecordConsumerGroupMetrics records consumer group metrics on the current span.
// Use this for one-off metric recording without maintaining tracker state.
//
// Example:
//
//	messaging.RecordConsumerGroupMetrics(ctx, messaging.ConsumerGroupState{
//	    GroupID:  "order-processor",
//	    MemberID: "consumer-1",
//	    IsActive: true,
//	})
func RecordConsumerGroupMetrics(ctx context.Context, state ConsumerGroupState) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	if state.GroupID != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.id", state.GroupID))
	}
	if state.MemberID != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.member_id", state.MemberID))
	}
	if state.GroupInstanceID != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.instance_id", state.GroupInstanceID))
	}
	if state.Generation >= 0 {
		span.SetAttributes(attribute.Int("messaging.consumer_group.generation", state.Generation))
	}
	span.SetAttributes(attribute.Bool("messaging.consumer_group.is_active", state.IsActive))
	if state.State != "" {
		span.SetAttributes(attribute.String("messaging.consumer_group.state", state.State))
	}
	span.SetAttributes(attribute.Int("messaging.consumer_group.assigned_partition_count", len(state.AssignedPartitions)))
}
