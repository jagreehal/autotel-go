// Part 7: Errors that explain themselves
//
// Six withdrawals, six reasons to refuse, and one screen for all of them.
//
// TODO: make each refusal say what happened, why, and what to do next, without
// showing anything that helps someone standing behind the customer.
//
//	go run ./workshop/07-errors-that-explain-themselves/exercise
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})

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

// screen is what the customer standing at the machine actually sees.
func screen(returned string) {
	fmt.Println("┌──────────────────────────────────────┐")
	fmt.Println("│ Transaction failed.                  │")
	fmt.Println("│ Contact your bank.                   │")
	fmt.Println("└──────────────────────────────────────┘")
	fmt.Printf("  (returned: %s)\n\n", returned)
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
	bank.ResetAuthAttempts()

	users := bank.Users()
	card := users[1].CardNumber

	scenarios := []struct {
		label, card, pin string
		amount           int64
	}{
		{"an unknown card", "4000999999", "1234", 10_000},
		{"a wrong PIN", card, "9999", 10_000},
		{"a second wrong PIN", card, "9999", 10_000},
		{"a third wrong PIN, and the machine keeps the card", card, "9999", 10_000},
		{"more than the balance", users[2].CardNumber, "1234", 999_999},
		{"over the daily limit", users[3].CardNumber, "1234", 40_000},
	}

	for _, scenario := range scenarios {
		fmt.Printf("\n── %s ──\n", scenario.label)
		if _, err := withdraw(ctx, scenario.card, scenario.pin, scenario.amount); err != nil {
			screen(err.Error())
		}
	}
}
