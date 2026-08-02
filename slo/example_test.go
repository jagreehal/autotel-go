package slo_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jagreehal/autotel-go/v2/slo"
)

func ExampleTracker() {
	tracker, err := slo.NewTracker(
		slo.Definition{
			Name:   "checkout.availability",
			Target: 0.99,
			Window: 30 * 24 * time.Hour,
		},
		slo.WithMetrics(false),
	)
	if err != nil {
		log.Fatal(err)
	}

	for range 99 {
		if _, err := tracker.Record(context.Background(), slo.OutcomeGood); err != nil {
			log.Fatal(err)
		}
	}
	snapshot, err := tracker.Record(context.Background(), slo.OutcomeBad)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("SLI: %.2f, burn rate: %.1f\n", *snapshot.SLI, snapshot.BurnRate)
	// Output: SLI: 0.99, burn rate: 1.0
}
