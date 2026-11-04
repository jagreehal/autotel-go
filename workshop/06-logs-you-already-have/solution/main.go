// check-count: 5 name = "atm.withdraw"
// check-log: pin ok
// check-log: withdrawal started
// check-stdout: "trace_id":

// Part 6: Our solution: set up first, in one place, then run the app
//
//	go run ./workshop/06-logs-you-already-have/solution
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

// The logger calls are the exercise's, unchanged.
func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		slog.InfoContext(ctx, "withdrawal started", "amount_pence", amountPence)

		user, err := bank.FindUserByCard(cardNumber)
		if err != nil {
			return bank.Receipt{}, err
		}

		slog.InfoContext(ctx, "user found", "user_id", user.ID)
		if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
			return bank.Receipt{}, err
		}
		slog.InfoContext(ctx, "pin ok")

		receipt, err := bank.Debit(bank.GetAccount(user.ID), amountPence)
		if err != nil {
			return bank.Receipt{}, err
		}

		slog.InfoContext(ctx, "debited", "transaction_id", receipt.TransactionID)

		return receipt, nil
	})
}

func main() {
	ctx := context.Background()

	// No Init and no slog.SetDefault here. setup did both, in that order.
	if err := setup(ctx); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := autotel.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	bank.ResetBalances()

	var machines sync.WaitGroup
	for index, user := range bank.Users()[:5] {
		machines.Go(func() {
			if _, err := withdraw(ctx, user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	machines.Wait()

	fmt.Print(`
Every line above now carries trace_id and span_id. Pick one trace_id and paste
it into the devtools search box:

  trace_id = "<a trace_id from above>"

The trace opens with its log lines attached, in order, under the span that
wrote each one.
`)
}
