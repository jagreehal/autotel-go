// Part 3: Attributes, not messages
//
// Every trace here is named atm.withdraw and most last about 80ms. They are
// indistinguishable. Support wants to know whether the slow withdrawals come from
// one machine, one model or one branch, and nothing in this data can answer that.
//
// TODO: put the facts of the withdrawal on the span instead of into strings, then
// answer the three questions in the README from the devtools search box rather
// than by reading code.
//
//	go run ./workshop/03-attributes-not-messages/exercise
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
	return autotel.Trace(ctx, "atm.withdraw", func(context.Context, autotel.Span) (bank.Receipt, error) {
		fmt.Printf("withdrawal at %s (%s)\n", m.id, m.model)

		user, err := bank.FindUserByCard(cardNumber)
		if err != nil {
			return bank.Receipt{}, err
		}
		fmt.Printf("customer %s\n", user.Name)

		if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
			return bank.Receipt{}, err
		}

		account := bank.GetAccount(user.ID)
		fmt.Printf("account %s, balance %dp\n", account.ID, account.BalancePence)

		receipt, err := bank.Debit(account, amountPence)
		if err != nil {
			return bank.Receipt{}, err
		}
		fmt.Printf("dispensed %dp, txn %s\n", amountPence, receipt.TransactionID)

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
}
