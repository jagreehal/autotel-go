// check-count: 5 name = "atm.withdraw"
// check: name = "atm.withdraw" and service = atm

// Part 2: Our solution: init once, wrap once, flush at the end
//
//	npx autotel-devtools   # in another terminal
//	go run ./workshop/02-your-first-span/solution   # then look at http://127.0.0.1:4318
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

// autotel.Trace starts a span, runs the function inside it, and ends the span
// when the function returns. Wrapping the whole body makes each call one span.
func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(context.Context, autotel.Span) (bank.Receipt, error) {
		user, err := bank.FindUserByCard(cardNumber)
		if err != nil {
			return bank.Receipt{}, err
		}
		if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
			return bank.Receipt{}, err
		}
		return bank.Debit(bank.GetAccount(user.ID), amountPence)
	})
}

func main() {
	ctx := context.Background()

	// One call, once per process, before any traced code runs. WithDevtools
	// points the exporter at http://127.0.0.1:4318, which is the receiver
	// `npx autotel-devtools` is running.
	if _, err := autotel.Init(ctx, autotel.WithService("atm"), autotel.WithDevtools()); err != nil {
		log.Fatal(err)
	}

	// Spans are batched, and Shutdown is what sends the last batch. It reports
	// an error when nothing received them.
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

	fmt.Println("5 withdrawals traced. Open the devtools tab.")
}
