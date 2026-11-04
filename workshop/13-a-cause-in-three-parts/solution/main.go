// check-count: 200 name = "atm.withdraw"
// check-count: 200 name = "atm.dispense"
// check-count: 16 name = "atm.dispense" and duration > 500
// check-count: 16 name = "atm.withdraw" and atm.software_version = "4.2.0" and atm.model = "Diebold-522" and withdrawal.amount_band = ">=5000"
// check-child: atm.dispense of atm.withdraw
// check-stdout: 16 slow of 200
// check-stdout: uses

// Part 13: Our solution: rank the fields, then test the combination
//
//	go run ./workshop/13-a-cause-in-three-parts/solution
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/analysis"
	"github.com/jagreehal/autotel-go/v2/workshop/13-a-cause-in-three-parts/dispenser"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

var machines = []dispenser.Machine{
	{ID: "atm_0431", Model: "NCR-6622", Branch: "bridge-street", SoftwareVersion: "4.1.3"},
	{ID: "atm_0912", Model: "NCR-6622", Branch: "kings-cross", SoftwareVersion: "4.1.3"},
	{ID: "atm_0977", Model: "NCR-6622", Branch: "canada-water", SoftwareVersion: "4.2.0"},
	{ID: "atm_1180", Model: "Diebold-522", Branch: "bridge-street", SoftwareVersion: "4.1.3"},
	{ID: "atm_1204", Model: "Diebold-522", Branch: "canada-water", SoftwareVersion: "4.1.3"},
	{ID: "atm_1251", Model: "Diebold-522", Branch: "bridge-street", SoftwareVersion: "4.2.0"},
	{ID: "atm_1266", Model: "Diebold-522", Branch: "kings-cross", SoftwareVersion: "4.2.0"},
	{ID: "atm_1330", Model: "Wincor-2100", Branch: "canada-water", SoftwareVersion: "4.1.3"},
	{ID: "atm_1342", Model: "Wincor-2100", Branch: "kings-cross", SoftwareVersion: "4.1.3"},
	{ID: "atm_1388", Model: "Wincor-2100", Branch: "bridge-street", SoftwareVersion: "4.2.0"},
}

var amounts = []int64{1000, 2000, 5000, 1500, 6000}

// observation is one withdrawal's fields and how long it took. The duration stays
// outside the fields, so it cannot rank itself.
type observation struct {
	fields     analysis.Event
	durationMs float64
}

// Kept alongside the spans so the analysis loop can run in this file too.
var (
	observedMu sync.Mutex
	observed   []observation
)

func withdraw(ctx context.Context, m dispenser.Machine, cardNumber string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		startedAt := time.Now()

		band := analysis.Bucket(float64(amountPence), []float64{2000, 5000})

		fields := analysis.Event{
			"atm.id":                 m.ID,
			"atm.model":              m.Model,
			"atm.branch":             m.Branch,
			"atm.software_version":   m.SoftwareVersion,
			"withdrawal.amount_band": band,
		}

		record.Set(map[string]any{
			"atm": map[string]any{
				"id":               m.ID,
				"model":            m.Model,
				"branch":           m.Branch,
				"software_version": m.SoftwareVersion,
			},
			"withdrawal": map[string]any{"amount_pence": amountPence, "amount_band": band},
		})

		defer func() {
			var refused *bank.Error
			if errors.As(err, &refused) {
				fields["withdrawal.result"] = "refused"
				fields["withdrawal.refusal_code"] = string(refused.Code)
				record.Set(map[string]any{"withdrawal": map[string]any{
					"result":       "refused",
					"refusal_code": string(refused.Code),
				}})
			}
			record.EmitNow()

			observedMu.Lock()
			defer observedMu.Unlock()
			observed = append(observed, observation{fields, float64(time.Since(startedAt).Microseconds()) / 1000})
		}()

		user, err := autotel.Trace(ctx, "bank.find_user_by_card", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.FindUserByCard(cardNumber)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		fields["user.id"] = user.ID
		record.Set(map[string]any{"user": map[string]any{"id": user.ID}})

		if _, err := autotel.Trace(ctx, "bank.verify_pin", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.VerifyPin(cardNumber, "1234")
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
		fields["account.shard"] = account.Shard
		record.Set(map[string]any{"account": map[string]any{"id": account.ID, "shard": account.Shard}})

		receipt, err = autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
		if err != nil {
			return bank.Receipt{}, err
		}

		autotel.TraceNoError(ctx, "atm.dispense", func(context.Context, autotel.Span) struct{} {
			dispenser.Dispense(m, amountPence)
			return struct{}{}
		})
		fields["withdrawal.result"] = "dispensed"
		record.Set(map[string]any{"withdrawal": map[string]any{"result": "dispensed"}})

		return receipt, nil
	})
}

// label is a field's value as text, the way a dashboard shows it.
func label(fields analysis.Event, field string) string {
	if value, ok := fields[field].(string); ok {
		return value
	}
	return "undefined"
}

// p95By is what a dashboard shows: one label at a time, one number per value, in
// the order each value first appears.
func p95By(field string) string {
	var order []string
	groups := map[string][]float64{}
	for _, o := range observed {
		value := label(o.fields, field)
		if _, seen := groups[value]; !seen {
			order = append(order, value)
		}
		groups[value] = append(groups[value], o.durationMs)
	}

	parts := make([]string, 0, len(order))
	for _, value := range order {
		durations := groups[value]
		slices.Sort(durations)
		rank := int(math.Ceil(float64(len(durations)) * 0.95))
		parts = append(parts, fmt.Sprintf("%s %.0fms", value, durations[rank-1]))
	}
	return strings.Join(parts, "   ")
}

// testCombinations prints the subset table and returns the smallest subset of the
// suspects that selects exactly the slow withdrawals, with the suspects themselves.
//
// Tied rows say each value is on every slow withdrawal, not whether one is enough
// to describe them. Testing every subset answers that.
func testCombinations(slowTotal int) (smallest, suspects [][2]string) {
	suspects = [][2]string{
		{"atm.software_version", "4.2.0"},
		{"atm.model", "Diebold-522"},
		{"withdrawal.amount_band", ">=5000"},
	}

	fmt.Println("\n  4.2.0  Diebold  >=5000   withdrawals   slow")

	for mask := 1; mask < 8; mask++ {
		var chosen [][2]string
		for index, suspect := range suspects {
			if mask&(1<<index) != 0 {
				chosen = append(chosen, suspect)
			}
		}

		matching, slowCount := 0, 0
		for _, o := range observed {
			if !slices.ContainsFunc(chosen, func(s [2]string) bool { return label(o.fields, s[0]) != s[1] }) {
				matching++
				if o.durationMs > 500 {
					slowCount++
				}
			}
		}

		if matching == slowTotal && slowCount == slowTotal && (smallest == nil || len(chosen) < len(smallest)) {
			smallest = chosen
		}

		tick := func(index int) string {
			if mask&(1<<index) != 0 {
				return "yes"
			}
			return " - "
		}
		fmt.Printf("  %-8s%-8s%-8s %11d   %4d\n", tick(0), tick(1), tick(2), matching, slowCount)
	}

	return smallest, suspects
}

func main() {
	ctx := context.Background()
	if _, err := autotel.Init(ctx, autotel.WithService("atm"), autotel.WithDevtools()); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := autotel.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	users := bank.Users()

	// Twenty mornings, ten withdrawals each. A fresh morning resets the daily
	// limit, so nobody is refused and every withdrawal reaches the dispenser.
	for start := 0; start < 200; start += 10 {
		bank.ResetBalances()

		var batch sync.WaitGroup
		for index := start; index < start+10; index++ {
			batch.Go(func() {
				m := machines[index%len(machines)]
				user := users[index%len(users)]
				// Each batch of ten uses one amount, so amount and machine vary
				// independently, the way they do in production.
				_, _ = withdraw(ctx, m, user.CardNumber, amounts[(start/10)%len(amounts)])
			})
		}
		batch.Wait()
	}

	fmt.Print("\nThe dashboard, p95 by one label at a time:\n\n")
	for _, field := range []string{"atm.software_version", "atm.model", "withdrawal.amount_band", "atm.branch"} {
		fmt.Printf("  %-24s %s\n", field, p95By(field))
	}

	// Step 3 of the loop: what separates slow from normal?
	var slow, normal []analysis.Event
	for _, o := range observed {
		if o.durationMs > 500 {
			slow = append(slow, o.fields)
		} else {
			normal = append(normal, o.fields)
		}
	}

	differences := analysis.CompareCohorts(analysis.Options{Outlier: slow, Baseline: normal})

	fmt.Printf("\n%d slow of %d. What separates them:\n\n", len(slow), len(observed))
	fmt.Println("  field                    value          in slow   in normal")
	for _, d := range differences[:min(6, len(differences))] {
		fmt.Printf("  %-24s %-14s %6.0f%%   %7.0f%%\n", d.Field, d.Value, d.OutlierFraction*100, d.BaselineFraction*100)
	}

	smallest, suspects := testCombinations(len(slow))
	if smallest == nil {
		fmt.Print(`
No subset selects exactly the slow withdrawals. The cohort needs a field
that is not in the suspect list.
`)
		return
	}

	terms := make([]string, 0, len(smallest))
	for _, s := range smallest {
		terms = append(terms, fmt.Sprintf("%s = %q", s[0], s[1]))
	}

	fmt.Printf(`
The smallest subset that selects exactly the slow withdrawals uses
%d of %d conditions. That describes the cohort, not the cause.
Check individual traces, then the code, then change it and rerun:

  %s
  name = "atm.dispense" and duration > 500
`, len(smallest), len(suspects), strings.Join(terms, " and "))
}
