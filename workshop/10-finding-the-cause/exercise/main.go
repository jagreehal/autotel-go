// Part 10: Finding the cause
//
// 200 withdrawals across the estate. A handful are slow. Nobody knows why, and
// nobody knows in advance which field explains it. That is the situation.
//
// This code is well instrumented by the standards of part 5. Run it, then use the
// devtools search box to find out what the slow withdrawals have in common.
//
// TODO: when the data will not answer the question, work out which field is
// missing, add it, and try again.
//
//	go run ./workshop/10-finding-the-cause/exercise
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
200 withdrawals sent. In the devtools search box:

  name = "atm.withdraw" and duration > 200

Some of them are slow. Now find out what they have in common. Add one value at a
time to that query, such as: and atm.model = "NCR-6622". Try atm.model, then
atm.branch, then withdrawal.amount_pence. Keep going until you run out of fields.
`)
}
