// Part 2: Your first span
//
// This is where part 1 left you: ids and JSON, maintained by hand.
//
// TODO 1: call autotel.Init once, at the top of main, before anything else runs.
// TODO 2: wrap the body of withdraw so each call becomes one span.
// TODO 3: make sure the spans leave the process before it exits.
//
//	npx autotel-devtools   # in another terminal, left open all workshop
//	go run ./workshop/02-your-first-span/exercise   # then look at http://127.0.0.1:4318
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

	if _, err := bank.VerifyPin(cardNumber, pin); err != nil {
		return bank.Receipt{}, err
	}

	receipt, err := bank.Debit(bank.GetAccount(user.ID), amountPence)
	if err != nil {
		return bank.Receipt{}, err
	}

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

}
