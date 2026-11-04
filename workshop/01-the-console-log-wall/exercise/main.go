// Part 1: The console log wall
//
// Five customers withdraw cash at the same moment. This is the instrumentation
// you already have. Run it, then try to answer the question in the README.
//
//	go run ./workshop/01-the-console-log-wall/exercise
package main

import (
	"fmt"
	"log"
	"sync"

	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

func withdraw(cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	fmt.Println("withdrawal started")

	user, err := bank.FindUserByCard(cardNumber)
	if err != nil {
		return bank.Receipt{}, err
	}
	fmt.Printf("user found: %s\n", user.Name)

	if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
		return bank.Receipt{}, err
	}
	fmt.Println("pin ok")

	account := bank.GetAccount(user.ID)
	fmt.Printf("account found, balance %d\n", account.BalancePence)

	receipt, err := bank.Debit(account, amountPence)
	if err != nil {
		return bank.Receipt{}, err
	}
	fmt.Printf("debited, new balance %d\n", receipt.BalancePence)

	fmt.Println("withdrawal complete")

	return receipt, nil
}

func main() {
	bank.ResetBalances()

	// Five machines, five customers, one process. Exactly as it is in
	// production, where nothing waits its turn.
	var machines sync.WaitGroup
	for index, user := range bank.Users()[:5] {
		machines.Go(func() {
			if _, err := withdraw(user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	machines.Wait()
}
