package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/jagreehal/autotel-go/v2/messaging"
)

func TestOrderingTracker_SequentialMessagesAreInOrder(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()

	for seq := int64(100); seq < 105; seq++ {
		got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
			ID:        string(rune('a' + seq - 100)),
			Sequence:  seq,
			Partition: 0,
			Topic:     "orders",
		})
		if got != messaging.OrderingOK {
			t.Errorf("sequence %d: expected OrderingOK, got %v", seq, got)
		}
	}

	if stats := tracker.Stats(); stats.TotalMessages != 5 {
		t.Errorf("expected 5 total messages, got %d", stats.TotalMessages)
	}
}

func TestOrderingTracker_DetectsDuplicate(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()
	msg := messaging.OrderedMessage{ID: "m1", Sequence: 1, Partition: 0, Topic: "orders"}

	if got := tracker.CheckAndTrack(ctx, msg); got != messaging.OrderingOK {
		t.Fatalf("first delivery: expected OrderingOK, got %v", got)
	}
	if got := tracker.CheckAndTrack(ctx, msg); got != messaging.OrderingDuplicate {
		t.Errorf("second delivery: expected OrderingDuplicate, got %v", got)
	}

	if stats := tracker.Stats(); stats.DuplicateCount != 1 {
		t.Errorf("expected 1 duplicate, got %d", stats.DuplicateCount)
	}
}

func TestOrderingTracker_DuplicateCallbackFires(t *testing.T) {
	var seen string
	tracker := messaging.NewOrderingTracker(
		messaging.WithOnDuplicate(func(_ context.Context, id string) { seen = id }),
	)
	ctx := context.Background()
	msg := messaging.OrderedMessage{ID: "m1", Sequence: 1, Topic: "orders"}

	tracker.CheckAndTrack(ctx, msg)
	tracker.CheckAndTrack(ctx, msg)

	if seen != "m1" {
		t.Errorf("expected the duplicate callback to fire with 'm1', got %q", seen)
	}
}

func TestOrderingTracker_DetectsGap(t *testing.T) {
	var gapSize int64
	tracker := messaging.NewOrderingTracker(
		messaging.WithOnGap(func(_ context.Context, _, _ int64, size int64) { gapSize = size }),
	)
	ctx := context.Background()

	tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m1", Sequence: 1, Topic: "orders"})
	got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m5", Sequence: 5, Topic: "orders"})

	if got != messaging.OrderingGap {
		t.Errorf("expected OrderingGap, got %v", got)
	}
	if gapSize != 3 {
		t.Errorf("expected a gap of 3 (sequences 2-4), got %d", gapSize)
	}

	stats := tracker.Stats()
	if stats.GapCount != 1 || stats.TotalGapMessages != 3 {
		t.Errorf("expected 1 gap covering 3 messages, got %d/%d", stats.GapCount, stats.TotalGapMessages)
	}
}

func TestOrderingTracker_DetectsOutOfOrder(t *testing.T) {
	var expectedSeq, actualSeq int64
	tracker := messaging.NewOrderingTracker(
		messaging.WithOnOutOfOrder(func(_ context.Context, expected, actual int64) {
			expectedSeq, actualSeq = expected, actual
		}),
	)
	ctx := context.Background()

	tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m5", Sequence: 5, Topic: "orders"})
	got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m3", Sequence: 3, Topic: "orders"})

	if got != messaging.OrderingOutOfOrder {
		t.Errorf("expected OrderingOutOfOrder, got %v", got)
	}
	if expectedSeq != 6 || actualSeq != 3 {
		t.Errorf("expected callback with expected=6 actual=3, got %d/%d", expectedSeq, actualSeq)
	}
}

func TestOrderingTracker_SequencesAreTrackedPerPartition(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()

	// Interleaving two partitions must not look like gaps or reordering.
	steps := []struct {
		id        string
		seq       int64
		partition int
	}{
		{"a1", 100, 0},
		{"b1", 500, 1},
		{"a2", 101, 0},
		{"b2", 501, 1},
	}

	for _, s := range steps {
		got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
			ID: s.id, Sequence: s.seq, Partition: s.partition, Topic: "orders",
		})
		if got != messaging.OrderingOK {
			t.Errorf("%s (partition %d, seq %d): expected OrderingOK, got %v",
				s.id, s.partition, s.seq, got)
		}
	}
}

func TestOrderingTracker_HighPartitionNumbersStayDistinct(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()

	// Partitions 10 and 200 must be tracked independently.
	for _, partition := range []int{10, 200} {
		got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{
			ID:        "first-" + string(rune('a'+partition%26)),
			Sequence:  1000,
			Partition: partition,
			Topic:     "orders",
		})
		if got != messaging.OrderingOK {
			t.Errorf("partition %d: expected the first message to initialise cleanly, got %v",
				partition, got)
		}
	}

	if stats := tracker.Stats(); stats.GapCount != 0 || stats.OutOfOrderCount != 0 {
		t.Errorf("expected distinct partitions to produce no gaps or reordering, got %+v", stats)
	}
}

func TestOrderingTracker_DeduplicationWindowEvicts(t *testing.T) {
	tracker := messaging.NewOrderingTracker(messaging.WithDeduplicationWindowSize(2))
	ctx := context.Background()

	// Sequence is -1 so only deduplication is exercised.
	for _, id := range []string{"m1", "m2", "m3"} {
		tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: id, Sequence: -1})
	}

	// m1 should have been evicted by the 2-entry window, so it is no longer a duplicate.
	if got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m1", Sequence: -1}); got != messaging.OrderingOK {
		t.Errorf("expected the evicted ID to no longer be treated as a duplicate, got %v", got)
	}
	// m3 is still inside the window.
	if got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "m3", Sequence: -1}); got != messaging.OrderingDuplicate {
		t.Errorf("expected a recent ID to still be a duplicate, got %v", got)
	}
}

func TestOrderingTracker_DeduplicationWindowExpiresByTime(t *testing.T) {
	tracker := messaging.NewOrderingTracker(
		messaging.WithDeduplicationWindowDuration(10 * time.Millisecond),
	)
	ctx := context.Background()
	msg := messaging.OrderedMessage{ID: "m1", Sequence: -1}

	tracker.CheckAndTrack(ctx, msg)
	time.Sleep(20 * time.Millisecond)

	if got := tracker.CheckAndTrack(ctx, msg); got != messaging.OrderingOK {
		t.Errorf("expected the ID to fall out of the time window, got %v", got)
	}
}

func TestOrderingTracker_EmptyMessageIDIsNeverDuplicate(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if got := tracker.CheckAndTrack(ctx, messaging.OrderedMessage{ID: "", Sequence: -1}); got != messaging.OrderingOK {
			t.Errorf("iteration %d: an empty ID must not be deduplicated, got %v", i, got)
		}
	}
}

func TestOrderingTracker_Reset(t *testing.T) {
	tracker := messaging.NewOrderingTracker()
	ctx := context.Background()
	msg := messaging.OrderedMessage{ID: "m1", Sequence: 1, Topic: "orders"}

	tracker.CheckAndTrack(ctx, msg)
	tracker.CheckAndTrack(ctx, msg)
	tracker.Reset()

	if stats := tracker.Stats(); stats.TotalMessages != 0 || stats.DuplicateCount != 0 {
		t.Errorf("expected stats to be cleared, got %+v", stats)
	}
	if got := tracker.CheckAndTrack(ctx, msg); got != messaging.OrderingOK {
		t.Errorf("expected dedup state to be cleared, got %v", got)
	}
}

func TestOrderingCheck_String(t *testing.T) {
	cases := map[messaging.OrderingCheck]string{
		messaging.OrderingOK:         "ok",
		messaging.OrderingOutOfOrder: "out_of_order",
		messaging.OrderingDuplicate:  "duplicate",
		messaging.OrderingGap:        "gap",
	}

	for check, want := range cases {
		if got := check.String(); got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	}
}

func TestOrderedMessage_PartitionKey(t *testing.T) {
	cases := []struct {
		msg  messaging.OrderedMessage
		want string
	}{
		{messaging.OrderedMessage{Topic: "orders", Partition: 0}, "orders:0"},
		{messaging.OrderedMessage{Topic: "orders", Partition: 200}, "orders:200"},
		{messaging.OrderedMessage{Partition: 7}, "partition:7"},
	}

	for _, tc := range cases {
		if got := tc.msg.PartitionKey(); got != tc.want {
			t.Errorf("PartitionKey() = %q, want %q", got, tc.want)
		}
	}
}

func TestCheckDuplicate(t *testing.T) {
	ctx := context.Background()
	seen := make(map[string]bool)

	if messaging.CheckDuplicate(ctx, seen, "m1") {
		t.Error("expected the first sighting not to be a duplicate")
	}
	if !messaging.CheckDuplicate(ctx, seen, "m1") {
		t.Error("expected the second sighting to be a duplicate")
	}
	if messaging.CheckDuplicate(ctx, seen, "") {
		t.Error("expected an empty ID never to be reported as a duplicate")
	}
}
