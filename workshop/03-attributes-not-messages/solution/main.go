// check-count: 9 name = "atm.withdraw"
// check-count: 6 atm.branch = "bridge-street"
// check: user.id = "u_007" and atm.id = "atm_0431"
// check: name = "atm.withdraw" and duration > 100 and user.id = "u_007"

// Part 3: Our solution: the facts go on the span
//
//	go run ./workshop/03-attributes-not-messages/solution
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

type machine struct{ id, model, branch string }

var machines = []machine{
	{"atm_0431", "NCR-6622", "bridge-street"},
	{"atm_0912", "NCR-6622", "kings-cross"},
	{"atm_1180", "Diebold-522", "bridge-street"},
}

func withdraw(ctx context.Context, m machine, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		// SetAttributes finds the active span in ctx, so a helper several calls
		// deep can add to it as long as ctx reaches it.
		autotel.SetAttributes(ctx, map[string]any{
			"atm.id":                  m.id,
			"atm.model":               m.model,
			"atm.branch":              m.branch,
			"withdrawal.amount_pence": amountPence,
		})

		user, err := bank.FindUserByCard(cardNumber)
		if err != nil {
			return bank.Receipt{}, err
		}
		autotel.SetAttribute(ctx, "user.id", user.ID)

		if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
			return bank.Receipt{}, err
		}

		account := bank.GetAccount(user.ID)
		autotel.SetAttributes(ctx, map[string]any{
			"account.id":            account.ID,
			"account.balance_pence": account.BalancePence,
		})

		receipt, err := bank.Debit(account, amountPence)
		if err != nil {
			return bank.Receipt{}, err
		}
		autotel.SetAttribute(ctx, "withdrawal.transaction_id", receipt.TransactionID)

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

	var running sync.WaitGroup
	for index, user := range bank.Users()[:9] {
		running.Go(func() {
			m := machines[index%len(machines)]
			if _, err := withdraw(ctx, m, user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	running.Wait()

	fmt.Print(`
9 withdrawals traced. In the devtools search box, try:

  atm.branch = "bridge-street"
  name = "atm.withdraw" and duration > 100
  user.id = "u_007"

None of those queries were written into the code. That is the point.
`)
}
