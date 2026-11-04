// Part 4: Which work deserves a span
//
// One span per withdrawal tells you a withdrawal was slow. It does not tell you
// which part of it was slow, and that is the question you actually have.
//
// Six operations run inside a withdrawal. Decide which of them get their own
// span, then wrap those and only those.
//
//  1. VerifyPin: checks the PIN, counts failures, can lock the card
//
//  2. FindUserByCard: looks the customer up
//
//  3. GetAccount: looks the account up
//
//  4. Debit: moves the money
//
//  5. chooseNotes: arithmetic over four denominations
//
//  6. formatPence: turns 4000 into "£40.00"
//
//     go run ./workshop/04-which-work-deserves-a-span/exercise
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

// chooseNotes is greedy note selection: arithmetic over a four-element slice.
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

// formatPence turns 4000 into "£40.00".
func formatPence(pence int64) string {
	return fmt.Sprintf("£%d.%02d", pence/100, pence%100)
}

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		autotel.SetAttribute(ctx, "withdrawal.amount_pence", amountPence)

		// TODO: give a span to the operations that earn one, and to nothing else.
		user, err := bank.FindUserByCard(cardNumber)
		if err != nil {
			return bank.Receipt{}, err
		}
		autotel.SetAttribute(ctx, "user.id", user.ID)

		if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
			return bank.Receipt{}, err
		}

		account := bank.GetAccount(user.ID)

		receipt, err := bank.Debit(account, amountPence)
		if err != nil {
			return bank.Receipt{}, err
		}

		autotel.SetAttributes(ctx, map[string]any{
			"account.id":                account.ID,
			"withdrawal.transaction_id": receipt.TransactionID,
		})

		notes := chooseNotes(amountPence)
		fmt.Printf("dispensing %s as %d notes\n", formatPence(amountPence), len(notes))

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
}
