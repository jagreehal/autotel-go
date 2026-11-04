// check-count: 10 name = "atm.withdraw"
// check-count: 8 withdrawal.result = "dispensed"
// check-count: 2 name = "atm.withdraw" and withdrawal.result = "refused" and withdrawal.refusal_code = "DAILY_LIMIT_EXCEEDED"
// check: withdrawal.refusal_code = "DAILY_LIMIT_EXCEEDED" and account.balance_pence = 42830 and user.id = "u_001"

// Part 5: Our solution: one snapshot per withdrawal
//
//	go run ./workshop/05-one-wide-event/solution
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

var branches = []string{"bridge-street", "kings-cross"}

func withdraw(ctx context.Context, branch, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		// One logger for this withdrawal, bound to the span in ctx. Every Set
		// lands on the same record.
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{
			"atm":        map[string]any{"branch": branch},
			"withdrawal": map[string]any{"amount_pence": amountPence},
		})

		// Deferred, so every return below reaches it: whichever way the
		// withdrawal goes, the outcome is recorded and the record emitted.
		defer func() {
			var refusal *bank.Error
			if errors.As(err, &refusal) {
				// The refusal is a fact about the withdrawal, so it goes on the
				// same record as everything else the withdrawal knew.
				record.Set(map[string]any{"withdrawal": map[string]any{
					"result":       "refused",
					"refusal_code": string(refusal.Code),
				}})
			} else {
				record.Set(map[string]any{"withdrawal": map[string]any{
					"result":         "dispensed",
					"transaction_id": receipt.TransactionID,
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
			return bank.VerifyPin(cardNumber, pin)
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
		record.Set(map[string]any{"account": map[string]any{
			"id":                    account.ID,
			"balance_pence":         account.BalancePence,
			"withdrawn_today_pence": account.WithdrawnTodayPence,
			"daily_limit_pence":     account.DailyLimitPence,
		}})

		return autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
	})
}

func attempt(ctx context.Context, index int, amountPence int64) {
	user := bank.Users()[index]
	// A refusal is an outcome, not a crash. The wide event already holds it.
	_, _ = withdraw(ctx, branches[index%len(branches)], user.CardNumber, "1234", amountPence)
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

	for index := range 8 {
		attempt(ctx, index, 10_000)
	}
	attempt(ctx, 0, 25_000)
	attempt(ctx, 3, 25_000)

	fmt.Print(`
10 withdrawals, 10 events. The question that needed three spans is now one
filter:

  withdrawal.result = "refused" and atm.branch = "bridge-street"

And the one nobody thought to ask in advance, which is the real test:

  withdrawal.refusal_code = "DAILY_LIMIT_EXCEEDED" and account.balance_pence > 40000
`)
}
