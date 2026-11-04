// Part 9: The dashboard
//
// Before anyone can investigate slow withdrawals, someone has to notice them.
// Operations want a chart of withdrawal time per branch and a count of withdrawals
// by outcome, kept for a year, at a cost that does not grow with traffic. The spans
// here are too detailed and too expensive to keep that long.
//
// TODO: record a histogram of how long each withdrawal took, split by branch, and a
// counter of withdrawals, split by branch and result.
//
//	go run ./workshop/09-the-dashboard/exercise
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

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

				// Refusals are expected; part 7 covered them.
				_, _ = withdraw(ctx, m, user.CardNumber, amountPence)
			})
		}
		batch.Wait()
	}

	fmt.Print(`
200 withdrawals sent, and nothing in devtools' Metrics view yet. Operations want
two charts: time per branch, and withdrawals by branch and result.
`)
}
