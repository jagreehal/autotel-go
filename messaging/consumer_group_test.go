package messaging_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/messaging"
)

// newSpanRecorder installs an in-memory tracer provider as the global provider.
func newSpanRecorder(t *testing.T) (trace.Tracer, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	return provider.Tracer("test"), recorder
}

func findSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, s := range recorder.Ended() {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("span %q was not recorded", name)
	return nil
}

func stringSliceAttribute(t *testing.T, recorder *tracetest.SpanRecorder, spanName, key string) []string {
	t.Helper()

	for _, kv := range findSpan(t, recorder, spanName).Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice()
		}
	}
	t.Fatalf("attribute %q not found on span %q", key, spanName)
	return nil
}

func int64Attribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) int64 {
	t.Helper()

	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsInt64()
		}
	}
	t.Fatalf("attribute %q not found", key)
	return 0
}

func assignments(topic string, partitions ...int) []messaging.PartitionAssignment {
	out := make([]messaging.PartitionAssignment, len(partitions))
	for i, p := range partitions {
		out[i] = messaging.PartitionAssignment{Topic: topic, Partition: p}
	}
	return out
}

func partitionSet(as []messaging.PartitionAssignment) map[int]bool {
	set := make(map[int]bool, len(as))
	for _, a := range as {
		set[a.Partition] = true
	}
	return set
}

func TestConsumerGroupTracker_AssignedUpdatesState(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker(
		messaging.WithConsumerGroupID("orders"),
		messaging.WithMemberID("consumer-1"),
	)

	tracker.RecordRebalance(context.Background(), messaging.RebalanceEvent{
		Type:       messaging.RebalanceAssigned,
		Partitions: assignments("orders", 0, 1),
		Timestamp:  time.Now(),
		Generation: 5,
	})

	state := tracker.State()
	if state.State != "stable" {
		t.Errorf("expected state 'stable', got %q", state.State)
	}
	if !state.IsActive {
		t.Error("expected the member to be active after assignment")
	}
	if state.Generation != 5 {
		t.Errorf("expected generation 5, got %d", state.Generation)
	}
	if len(state.AssignedPartitions) != 2 {
		t.Errorf("expected 2 assigned partitions, got %d", len(state.AssignedPartitions))
	}
}

func TestConsumerGroupTracker_RevokeRemovesOnlyNamedPartitions(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker(messaging.WithConsumerGroupID("orders"))
	ctx := context.Background()

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceAssigned,
		Partitions: assignments("orders", 0, 10, 17, 200),
		Timestamp:  time.Now(),
	})

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceRevoked,
		Partitions: assignments("orders", 10),
		Timestamp:  time.Now(),
	})

	remaining := partitionSet(tracker.State().AssignedPartitions)
	if len(remaining) != 3 {
		t.Fatalf("expected 3 partitions to remain, got %d (%v)", len(remaining), remaining)
	}
	if remaining[10] {
		t.Error("expected partition 10 to be revoked")
	}
	for _, want := range []int{0, 17, 200} {
		if !remaining[want] {
			t.Errorf("expected partition %d to survive revocation of partition 10", want)
		}
	}
}

func TestConsumerGroupTracker_PartitionNumbersRenderAsDecimal(t *testing.T) {
	// Regression: partition numbers were encoded with string(rune('0'+n)), so
	// partition 10 was emitted as ":", 17 as "A" and 200 as "ø". Anything above
	// 9 reached the backend as garbage, and topics with >10 partitions are the
	// common case.
	tracer, recorder := newSpanRecorder(t)

	ctx, span := tracer.Start(context.Background(), "rebalance")
	tracker := messaging.NewConsumerGroupTracker()
	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceAssigned,
		Partitions: assignments("orders", 0, 10, 17, 200),
		Timestamp:  time.Now(),
	})
	span.End()

	got := stringSliceAttribute(t, recorder, "rebalance", "messaging.consumer_group.rebalance.partitions")

	want := []string{"orders:0", "orders:10", "orders:17", "orders:200"}
	if len(got) != len(want) {
		t.Fatalf("expected %d partition keys, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("partition key %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestConsumerGroupTracker_PartitionLagUsesStaticAttributeKeys(t *testing.T) {
	// Regression: lag attribute keys were built by concatenating the topic and a
	// rune-encoded partition into the key itself, producing unbounded attribute-key
	// cardinality plus garbage characters. Topic and partition belong in values.
	tracer, recorder := newSpanRecorder(t)

	ctx, span := tracer.Start(context.Background(), "lag")
	tracker := messaging.NewConsumerGroupTracker()
	tracker.RecordPartitionLag(ctx, messaging.PartitionLagInfo{
		Topic:         "orders",
		Partition:     42,
		CurrentOffset: 1000,
		EndOffset:     1050,
		Lag:           50,
		Timestamp:     time.Now(),
	})
	span.End()

	recorded := findSpan(t, recorder, "lag")
	for _, kv := range recorded.Attributes() {
		key := string(kv.Key)
		if strings.Contains(key, "orders") {
			t.Errorf("topic must not appear in an attribute key, found %q", key)
		}
		if strings.Contains(key, "42") {
			t.Errorf("partition must not appear in an attribute key, found %q", key)
		}
	}

	if got := int64Attribute(t, recorded, "messaging.consumer_group.lag.lag"); got != 50 {
		t.Errorf("expected lag 50 as an attribute value, got %d", got)
	}
}

func TestConsumerGroupTracker_RevokeAllEmptiesGroup(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker()
	ctx := context.Background()

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceAssigned,
		Partitions: assignments("orders", 0, 1),
		Timestamp:  time.Now(),
	})
	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceRevoked,
		Partitions: assignments("orders", 0, 1),
		Timestamp:  time.Now(),
	})

	state := tracker.State()
	if len(state.AssignedPartitions) != 0 {
		t.Errorf("expected no assigned partitions, got %v", state.AssignedPartitions)
	}
	if state.State != "empty" {
		t.Errorf("expected state 'empty', got %q", state.State)
	}
}

func TestConsumerGroupTracker_LostMarksInactive(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker()
	ctx := context.Background()

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceAssigned,
		Partitions: assignments("orders", 0),
		Timestamp:  time.Now(),
	})
	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceLost,
		Partitions: assignments("orders", 0),
		Timestamp:  time.Now(),
	})

	state := tracker.State()
	if state.IsActive {
		t.Error("expected the member to be inactive after losing partitions")
	}
	if state.State != "dead" {
		t.Errorf("expected state 'dead', got %q", state.State)
	}
}

func TestConsumerGroupTracker_SamePartitionDifferentTopics(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker()
	ctx := context.Background()

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type: messaging.RebalanceAssigned,
		Partitions: []messaging.PartitionAssignment{
			{Topic: "orders", Partition: 1},
			{Topic: "shipments", Partition: 1},
		},
		Timestamp: time.Now(),
	})
	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type:       messaging.RebalanceRevoked,
		Partitions: []messaging.PartitionAssignment{{Topic: "orders", Partition: 1}},
		Timestamp:  time.Now(),
	})

	remaining := tracker.State().AssignedPartitions
	if len(remaining) != 1 || remaining[0].Topic != "shipments" {
		t.Errorf("expected only shipments:1 to remain, got %v", remaining)
	}
}

func TestConsumerGroupTracker_CallbacksFire(t *testing.T) {
	var rebalances, assigned, revoked int

	tracker := messaging.NewConsumerGroupTracker(
		messaging.WithOnRebalance(func(messaging.RebalanceEvent) { rebalances++ }),
		messaging.WithOnPartitionsAssigned(func([]messaging.PartitionAssignment) { assigned++ }),
		messaging.WithOnPartitionsRevoked(func([]messaging.PartitionAssignment) { revoked++ }),
	)
	ctx := context.Background()

	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type: messaging.RebalanceAssigned, Partitions: assignments("orders", 0), Timestamp: time.Now(),
	})
	tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
		Type: messaging.RebalanceRevoked, Partitions: assignments("orders", 0), Timestamp: time.Now(),
	})

	if rebalances != 2 || assigned != 1 || revoked != 1 {
		t.Errorf("expected rebalances=2 assigned=1 revoked=1, got %d/%d/%d", rebalances, assigned, revoked)
	}
}

func TestConsumerGroupTracker_StateReturnsCopy(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker()
	tracker.RecordRebalance(context.Background(), messaging.RebalanceEvent{
		Type: messaging.RebalanceAssigned, Partitions: assignments("orders", 0), Timestamp: time.Now(),
	})

	state := tracker.State()
	state.AssignedPartitions[0].Partition = 999

	if tracker.State().AssignedPartitions[0].Partition == 999 {
		t.Error("expected State() to return a copy the caller cannot mutate")
	}
}

func TestConsumerGroupTracker_ConcurrentAccessIsRaceFree(t *testing.T) {
	tracker := messaging.NewConsumerGroupTracker()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.RecordRebalance(ctx, messaging.RebalanceEvent{
				Type:       messaging.RebalanceAssigned,
				Partitions: assignments("orders", i),
				Timestamp:  time.Now(),
			})
			tracker.RecordHeartbeat(ctx, true, time.Millisecond)
			_ = tracker.State()
		}(i)
	}
	wg.Wait()
}
