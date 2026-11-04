// check-stdout: "event":"withdrawal.completed"
// check-stdout: "withdrawal_id":
// check-stdout: "duration_ms":

// Part 1: Our solution: give every withdrawal an id, and log key-values
//
// No library yet. This is what you would write yourself once the interleaved
// output beat you, and it is worth writing because of what it still cannot do.
//
//	go run ./workshop/01-the-console-log-wall/solution
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

// logLine prints one line, one JSON object, with one id tying the object to its
// withdrawal.
func logLine(withdrawalID, event string, fields map[string]any) {
	line := map[string]any{"t": time.Now().UnixMilli(), "withdrawal_id": withdrawalID, "event": event}
	maps.Copy(line, fields)

	encoded, err := json.Marshal(line)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Println(string(encoded))
}

func withdraw(cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	withdrawalID := fmt.Sprintf("%08x", rand.Uint32())
	startedAt := time.Now()
	logLine(withdrawalID, "withdrawal.started", map[string]any{"amount_pence": amountPence})

	user, err := bank.FindUserByCard(cardNumber)
	if err != nil {
		return bank.Receipt{}, err
	}
	logLine(withdrawalID, "user.found", map[string]any{"user_id": user.ID})

	if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
		return bank.Receipt{}, err
	}
	logLine(withdrawalID, "pin.verified", nil)

	account := bank.GetAccount(user.ID)
	logLine(withdrawalID, "account.found", map[string]any{"account_id": account.ID})

	receipt, err := bank.Debit(account, amountPence)
	if err != nil {
		return bank.Receipt{}, err
	}
	logLine(withdrawalID, "account.debited", map[string]any{"transaction_id": receipt.TransactionID})

	logLine(withdrawalID, "withdrawal.completed", map[string]any{"duration_ms": time.Since(startedAt).Milliseconds()})

	return receipt, nil
}

func main() {
	bank.ResetBalances()

	var machines sync.WaitGroup
	for index, user := range bank.Users()[:5] {
		machines.Go(func() {
			if _, err := withdraw(user.CardNumber, "1234", int64(index+1)*2000); err != nil {
				log.Print(err)
			}
		})
	}
	machines.Wait()

	fmt.Print(`
Now you can grep one withdrawal out of the noise:

  go run ./workshop/01-the-console-log-wall/solution > /tmp/part1.log
  grep <withdrawal_id> /tmp/part1.log

Four things this still will not tell you:

  1. How long VerifyPin took. You timed the whole withdrawal, not its parts.
  2. Which step was slow when one withdrawal is slow.
  3. What happened when the process died before the last line was written.
  4. Anything at all once the debit moves to another service.

Every one of those is fixed by the same change, and you have already written
most of it. Part 2.
`)
}
