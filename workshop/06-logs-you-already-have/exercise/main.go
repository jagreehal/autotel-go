// Part 6: Logs you already have
//
// The bank's code already logs with log/slog, and nobody is going to delete four
// hundred log lines to adopt OpenTelemetry. This file calls autotel.Init on the
// first line of main, every withdrawal is traced, and still no log line carries a
// trace id.
//
// TODO: make every log line carry the trace_id and span_id of the withdrawal that
// wrote it, without adding either field by hand.
//
//	go run ./workshop/06-logs-you-already-have/exercise
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

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

	// The first line of main. Every withdrawal below runs inside a span.
	if _, err := autotel.Init(ctx, autotel.WithService("atm"), autotel.WithDevtools()); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := autotel.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	// The team's logger, set up the way it has been for three years.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	bank.ResetBalances()

	// Part 1's five machines, logging into one stream again.
	var machines sync.WaitGroup
	for index, user := range bank.Users()[:5] {
		machines.Go(func() {
			if _, err := withdraw(ctx, user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	machines.Wait()
}
