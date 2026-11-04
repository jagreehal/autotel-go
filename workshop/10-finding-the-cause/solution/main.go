// check-count: 200 name = "atm.withdraw"
// check-count: 16 name = "atm.withdraw" and account.shard = "legacy-core"
// check-count: 16 name = "atm.withdraw" and duration > 200
// check: withdrawal.amount_band = "1000-2000"
// check-stdout: account.shard
// check-stdout: 16 slow of 200

// Part 10: Our solution: record the shard, then let the data rank itself
//
//	go run ./workshop/10-finding-the-cause/solution
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/analysis"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

type machine struct{ id, model, branch string }

var machines = []machine{
	{"atm_0431", "NCR-6622", "bridge-street"},
	{"atm_0912", "NCR-6622", "kings-cross"},
	{"atm_1180", "Diebold-522", "bridge-street"},
	{"atm_1204", "Diebold-522", "canada-water"},
	{"atm_1330", "Wincor-2100", "canada-water"},
}

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

func withdraw(ctx context.Context, m machine, cardNumber string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		startedAt := time.Now()

		// Raw amounts never repeat often enough to describe a group, so the
		// bucket is what makes the number groupable at all.
		band := analysis.Bucket(float64(amountPence), []float64{1000, 2000, 5000})

		fields := analysis.Event{
			"atm.id":                 m.id,
			"atm.model":              m.model,
			"atm.branch":             m.branch,
			"withdrawal.amount_band": band,
		}

		record.Set(map[string]any{
			"atm":        map[string]any{"id": m.id, "model": m.model, "branch": m.branch},
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
		// The field that was missing. It is a property of the account, not of
		// the request, which is exactly why nobody thought to record it.
		fields["account.shard"] = account.Shard
		record.Set(map[string]any{"account": map[string]any{"id": account.ID, "shard": account.Shard}})

		receipt, err = autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		fields["withdrawal.result"] = "dispensed"
		record.Set(map[string]any{"withdrawal": map[string]any{"result": "dispensed"}})

		return receipt, nil
	})
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

	bank.ResetBalances()
	users := bank.Users()

	// Ten machines busy at once, so 200 withdrawals take two seconds rather than
	// twenty. The estate is not polite enough to queue either.
	for start := 0; start < 200; start += 10 {
		var batch sync.WaitGroup
		for index := start; index < start+10; index++ {
			batch.Go(func() {
				m := machines[index%len(machines)]
				user := users[index%len(users)]
				// Refusals are expected.
				_, _ = withdraw(ctx, m, user.CardNumber, 1000+int64(index%4)*500)
			})
		}
		batch.Wait()
	}

	// Step 2 of the core analysis loop: name the group that looks wrong.
	var slow, normal []analysis.Event
	for _, o := range observed {
		if o.durationMs > 200 {
			slow = append(slow, o.fields)
		} else {
			normal = append(normal, o.fields)
		}
	}

	// Step 3, which is the part worth automating: what separates them?
	differences := analysis.CompareCohorts(analysis.Options{Outlier: slow, Baseline: normal})

	fmt.Printf("\n%d slow of %d. What separates them:\n\n", len(slow), len(observed))
	fmt.Println("  field                        value          in slow   in normal")

	for _, d := range differences[:min(6, len(differences))] {
		fmt.Printf("  %-28s %-14s %6.0f%%   %7.0f%%\n", d.Field, d.Value, d.OutlierFraction*100, d.BaselineFraction*100)
	}

	fmt.Print(`
The top row is the answer, and it is a field the exercise never recorded.
Confirm it against individual traces before believing it:

  account.shard = "legacy-core"
  name = "atm.withdraw" and duration > 200
`)
}
