// check-count: 200 name = "atm.withdraw"
// check-metric: atm.withdraw.duration
// check-metric: atm.withdrawals

// Part 9: Our solution: two instruments, recorded once per withdrawal
//
//	go run ./workshop/09-the-dashboard/solution
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jagreehal/autotel-go/v2"
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

func withdraw(ctx context.Context, m machine, cardNumber string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{
			"atm":        map[string]any{"id": m.id, "model": m.model, "branch": m.branch},
			"withdrawal": map[string]any{"amount_pence": amountPence},
		})

		defer func() {
			var refused *bank.Error
			if errors.As(err, &refused) {
				record.Set(map[string]any{"withdrawal": map[string]any{
					"result":       "refused",
					"refusal_code": string(refused.Code),
				}})
			}
			record.EmitNow()
		}()

		user, err := autotel.Trace(ctx, "bank.find_user_by_card", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.FindUserByCard(cardNumber)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"user": map[string]any{"id": user.ID}})

		if _, err := autotel.Trace(ctx, "bank.verify_pin", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.VerifyPin(cardNumber, "1234")
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
		record.Set(map[string]any{"account": map[string]any{"id": account.ID}})

		receipt, err = autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
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
				amountPence := 1000 + int64(index%4)*500

				startedAt := time.Now()
				_, err := withdraw(ctx, m, user.CardNumber, amountPence)

				// Labels are the only way to split a metric later, and each
				// distinct combination becomes its own series. Branch and result
				// have a handful of values between them. A user id here would
				// mean one series per customer.
				outcome := "dispensed"
				if err != nil {
					outcome = "refused"
				}
				meter := autotel.Meter()
				// A histogram keeps a count per time range (a bucket), not each
				// duration.
				meter.Histogram(ctx, "atm.withdraw.duration", float64(time.Since(startedAt).Microseconds())/1000,
					map[string]any{"atm.branch": m.branch})
				// A counter only goes up. The dashboard turns it into
				// withdrawals per minute.
				meter.Counter(ctx, "atm.withdrawals", 1,
					map[string]any{"atm.branch": m.branch, "withdrawal.result": outcome})
			})
		}
		// Refusals are expected; part 7 covered them.
		batch.Wait()
	}

	fmt.Print(`
200 withdrawals, two metrics. Open devtools' Metrics view:

  atm.withdraw.duration   one series per branch
  atm.withdrawals         one series per branch and result

Compare the branches. Two of them look slow. Part 10 asks whether they are.
`)
}
