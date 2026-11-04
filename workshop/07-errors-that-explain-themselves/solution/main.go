// check-count: 6 name = "atm.withdraw" and status = ERROR and withdrawal.result = "refused"
// check: name = "atm.withdraw" and withdrawal.refusal_code = "CARD_RETAINED" and auth.attempts = 3
// check: name = "atm.withdraw" and error.code = "DAILY_LIMIT_EXCEEDED" and error.status = 429 and account.daily_limit_pence = 30000
// check: name = "bank.verify_pin" and status = ERROR
// check-stdout: Daily limit reached

// Part 7: Our solution: one translation at the boundary
//
//	go run ./workshop/07-errors-that-explain-themselves/solution
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

// refusal is one table, one case per code, written once. Why explains, Fix is the
// next action, Status is what a caller branches on, Link is where the agent on
// the phone goes. None of them names a balance, a PIN or a card number.
func refusal(code bank.Code) *autotel.StructuredError {
	refused := &autotel.StructuredError{Code: string(code)}

	switch code {
	case bank.CardNotRecognised:
		refused.Message = "This card cannot be used here"
		refused.Why = "The card number is not one this network recognises."
		refused.Fix = "Check the card is the right way round, or use a machine on your own network."
		refused.Status = 404
		refused.Link = "https://bank.example/help/card-not-recognised"
	case bank.PinIncorrect:
		refused.Message = "Incorrect PIN"
		refused.Why = "The PIN entered does not match this card."
		refused.Fix = "Try again. After three wrong attempts the machine keeps the card."
		refused.Status = 401
		refused.Link = "https://bank.example/help/pin"
	case bank.CardRetained:
		refused.Message = "Card retained"
		refused.Why = "Three incorrect PINs were entered on this card."
		refused.Fix = "Call the number on your statement to have a replacement sent."
		refused.Status = 423
		refused.Link = "https://bank.example/help/card-retained"
	case bank.InsufficientFunds:
		refused.Message = "Not enough in the account"
		refused.Why = "The amount requested is more than the available balance."
		refused.Fix = "Try a smaller amount, or check for pending transactions in the app."
		refused.Status = 402
		refused.Link = "https://bank.example/help/balance"
	case bank.DailyLimitExceeded:
		refused.Message = "Daily limit reached"
		refused.Why = "This amount would take today’s total past the £300 daily cash limit."
		refused.Fix = "The limit resets at midnight. Larger amounts can be arranged in branch."
		refused.Status = 429
		refused.Link = "https://bank.example/help/daily-limit"
	default:
		// LedgerUnavailable, and any code added later: Go's switch is not
		// exhaustive, so a new code lands here rather than failing to compile.
		refused.Message = "The machine cannot reach your bank"
		refused.Why = "The ledger did not answer in time. No money has left the account."
		refused.Fix = "Wait a minute and try again. Nothing was debited."
		refused.Status = 503
		refused.Link = "https://bank.example/help/try-again"
	}

	return refused
}

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})

		// The translation, in one place. It runs after every return below and
		// replaces the error the caller sees, so nothing the screen must not
		// show can leave this function.
		defer func() {
			var refused *bank.Error
			if errors.As(err, &refused) {
				record.Set(map[string]any{"withdrawal": map[string]any{
					"result":       "refused",
					"refusal_code": string(refused.Code),
				}})
				// The domain's own detail (attempts remaining, limits) is safe
				// to keep in telemetry and is what the on-call engineer wants.
				record.Set(refused.Detail)
				err = refusal(refused.Code)
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

// screen shows the error the caller got back. ParseError reads any error as these
// fields, so there is nothing to parse by hand.
func screen(err error) {
	parsed := autotel.ParseError(err)
	row := func(text string) string {
		runes := []rune(text)
		runes = runes[:min(len(runes), 56)]
		return fmt.Sprintf("│ %-56s │", string(runes))
	}
	rule := strings.Repeat("─", 58)

	fmt.Printf("┌%s┐\n", rule)
	fmt.Println(row(parsed.Message))
	fmt.Printf("├%s┤\n", rule)
	for _, text := range []string{parsed.Why, parsed.Fix} {
		if text != "" {
			fmt.Println(row(text))
		}
	}
	fmt.Printf("└%s┘\n", rule)
	fmt.Printf("  status %d  ·  %s\n\n", parsed.Status, parsed.Link)
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
			screen(err)
		}
	}

	fmt.Print(`Every refusal above is one case in refusal(), one error, and one event in
devtools. Try:

  withdrawal.result = "refused"
  status = ERROR
`)
}
