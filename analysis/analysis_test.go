package analysis

import (
	"math"
	"testing"
)

// Slow withdrawals share a shard nobody queried for, and the ranking puts it
// first.
func TestCompareCohortsFindsThePlantedCause(t *testing.T) {
	var slow, normal []Event
	for i := range 200 {
		shard := "core"
		if i%25 == 6 {
			shard = "legacy-core"
		}
		event := Event{
			"atm.branch":     []string{"bridge-street", "kings-cross", "canada-water"}[i%3],
			"account.shard":  shard,
			"transaction.id": i, // unique per event, so it must be skipped
		}
		if shard == "legacy-core" {
			slow = append(slow, event)
		} else {
			normal = append(normal, event)
		}
	}

	got := CompareCohorts(Options{Outlier: slow, Baseline: normal})
	if len(got) == 0 || got[0].Field != "account.shard" || got[0].Value != "legacy-core" || got[0].OutlierFraction != 1 {
		t.Fatalf("top result %+v, want account.shard=legacy-core in every slow event", got)
	}
	for _, d := range got {
		if d.Field == "transaction.id" {
			t.Error("a near-unique field was ranked")
		}
	}

	if CompareCohorts(Options{Outlier: slow}) != nil {
		t.Error("an empty baseline produced results")
	}
}

func TestBucket(t *testing.T) {
	bounds := []float64{5000, 1000, 2000} // out of order on purpose
	for value, want := range map[float64]string{
		500: "<1000", 1000: "1000-2000", 2500: "2000-5000", 5000: ">=5000", math.NaN(): "unknown",
	} {
		if got := Bucket(value, bounds); got != want {
			t.Errorf("Bucket(%v) = %q, want %q", value, got, want)
		}
	}
	if Bucket(1, nil) != "unknown" {
		t.Error("no boundaries should be unknown")
	}
}
