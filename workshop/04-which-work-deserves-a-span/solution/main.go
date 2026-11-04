// check-count: 8 name = "atm.withdraw"
// check-count: 8 name = "bank.debit"
// check-child: bank.debit of atm.withdraw
// check-child: bank.verify_pin of atm.withdraw
// check-child: bank.find_user_by_card of atm.withdraw
// check-child: bank.get_account of atm.withdraw
// check: name = "atm.withdraw" and withdrawal.display_amount = "£20.00" and withdrawal.note_count = 1

// Part 4: Our solution: four spans, not six
//
//	go run ./workshop/04-which-work-deserves-a-span/solution
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

var denominations = []int64{5000, 2000, 1000, 500}

// chooseNotes gets no span: sub-millisecond, cannot fail, nothing to group by.
func chooseNotes(amountPence int64) []int64 {
	var notes []int64
	remaining := amountPence
	for _, note := range denominations {
		for remaining >= note {
			notes = append(notes, note)
			remaining -= note
		}
	}
	return notes
}

// formatPence gets no span either. It is a fmt.Sprintf call.
func formatPence(pence int64) string {
	return fmt.Sprintf("£%d.%02d", pence/100, pence%100)
}

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		autotel.SetAttribute(ctx, "withdrawal.amount_pence", amountPence)

		// autotel.Trace wraps a function you did not write. The bank stays free
		// of instrumentation; the seam is here, at the point of use.
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

		// GetAccount cannot fail, so TraceNoError is enough.
		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})

		// The interesting one. Money moved, so the span carries what moved.
		receipt, err := autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
		if err != nil {
			return bank.Receipt{}, err
		}

		// The two operations without spans still leave a trace: their result is
		// an attribute on the span that is already here.
		autotel.SetAttributes(ctx, map[string]any{
			"account.id":                account.ID,
			"withdrawal.transaction_id": receipt.TransactionID,
			"withdrawal.note_count":     len(chooseNotes(amountPence)),
			"withdrawal.display_amount": formatPence(amountPence),
		})

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

	var machines sync.WaitGroup
	for index, user := range bank.Users()[:8] {
		machines.Go(func() {
			if _, err := withdraw(ctx, user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	machines.Wait()

	fmt.Print(`
8 withdrawals, 5 spans each. Open one trace in devtools: the waterfall now says
where the time went. Find the slow one with user.id = "u_007" and compare it with
any other.
`)
}
