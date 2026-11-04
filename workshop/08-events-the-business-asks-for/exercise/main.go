// Part 8: The questions the business asks
//
// Three questions arrived this week, none of them from an engineer:
//
//  1. How much cash left the estate at bridge-street yesterday?
//  2. Of the customers we refused on the daily limit, how many had the money?
//  3. When we refuse someone, do they try again for a smaller amount?
//
// Question 2 you can already answer, from part 5. The other two are about what
// people did, they are asked every week, and the person asking does not have a
// devtools tab.
//
// TODO: emit a business event for each completed and each refused withdrawal, and
// send it somewhere the product team could actually read.
//
//	go run ./workshop/08-events-the-business-asks-for/exercise
package main

import (
	"context"
	"errors"
	"log"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

func withdraw(ctx context.Context, branch, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{
			"atm":        map[string]any{"branch": branch},
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
			return bank.VerifyPin(cardNumber, pin)
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
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

func attempt(ctx context.Context, index int, branch string, amountPence int64) {
	// Refused; the record above already says why.
	_, _ = withdraw(ctx, branch, bank.Users()[index].CardNumber, "1234", amountPence)
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

	for index := range 6 {
		branch := "bridge-street"
		if index%2 == 1 {
			branch = "kings-cross"
		}
		attempt(ctx, index, branch, 10_000)
	}

	// Customer 0 comes back for too much, is refused, and tries again smaller.
	attempt(ctx, 0, "bridge-street", 25_000)
	attempt(ctx, 0, "bridge-street", 15_000)
}
