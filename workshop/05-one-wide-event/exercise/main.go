// Part 5: One wide event per request
//
// Two withdrawals in here fail on the daily limit. To explain one of them you need
// the branch and the balance (attributes on the parent span) and the refusal code,
// which exists only inside the error a child span returned.
//
// TODO: emit one event per withdrawal carrying everything the withdrawal knew, so
// that answering "which customers hit the daily limit at bridge-street" is one
// filter and not a join.
//
//	go run ./workshop/05-one-wide-event/exercise
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
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		autotel.SetAttributes(ctx, map[string]any{
			"atm.branch":              branch,
			"withdrawal.amount_pence": amountPence,
		})

		user, err := autotel.Trace(ctx, "bank.find_user_by_card", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.FindUserByCard(cardNumber)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		autotel.SetAttribute(ctx, "user.id", user.ID)

		if _, err := autotel.Trace(ctx, "bank.verify_pin", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.VerifyPin(cardNumber, pin)
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
		autotel.SetAttribute(ctx, "account.balance_pence", account.BalancePence)

		return autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
	})
}

// code reads the bank's refusal code out of an error.
func code(err error) bank.Code {
	var refused *bank.Error
	if errors.As(err, &refused) {
		return refused.Code
	}
	return ""
}

func attempt(ctx context.Context, index int, amountPence int64) {
	user := bank.Users()[index]
	if _, err := withdraw(ctx, branches[index%len(branches)], user.CardNumber, "1234", amountPence); err != nil {
		fmt.Printf("refused: %s %s\n", user.ID, code(err))
	}
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
	// Two customers come back for more and hit the £300 daily limit.
	attempt(ctx, 0, 25_000)
	attempt(ctx, 3, 25_000)
}
