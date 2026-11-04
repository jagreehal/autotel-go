// Part 11: One trace, three services
//
// Nothing about the withdrawal changed. The four steps that used to be function
// calls are now HTTP calls to two other processes, each with its own Init and its
// own service name.
//
// TODO: run it, look at devtools, and work out why one withdrawal is now three
// unrelated traces, then make it one again.
//
//	go run ./workshop/11-one-trace-three-services/exercise
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const (
	authPort     = 7101
	accountsPort = 7102
)

var (
	authURL     = fmt.Sprintf("http://127.0.0.1:%d", authPort)
	accountsURL = fmt.Sprintf("http://127.0.0.1:%d", accountsPort)
)

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		record := autotel.NewRequestLogger(ctx)
		defer record.EmitNow()
		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})
		fmt.Printf("[atm]              trace %s\n", autotel.GetTraceID(ctx))

		request := harness.NewRequest(ctx, http.MethodPost, authURL+"/card", map[string]string{"card": cardNumber})
		var user harness.CardReply
		if err := harness.Send(request, &user); err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"user": map[string]any{"id": user.ID}})

		request = harness.NewRequest(ctx, http.MethodPost, authURL+"/verify", map[string]string{"card": cardNumber, "pin": pin})
		if err := harness.Send(request, nil); err != nil {
			return bank.Receipt{}, err
		}

		request = harness.NewRequest(ctx, http.MethodGet, accountsURL+"/account?userId="+user.ID, nil)
		var account bank.Account
		if err := harness.Send(request, &account); err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"account": map[string]any{"id": account.ID, "shard": account.Shard}})

		request = harness.NewRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/debit?userId=%s&amountPence=%d", accountsURL, user.ID, amountPence), nil)
		var receipt bank.Receipt
		if err := harness.Send(request, &receipt); err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"withdrawal": map[string]any{"result": "dispensed"}})

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

	if err := harness.StartServices("services", authPort, accountsPort); err != nil {
		log.Print(err)
		return
	}
	defer harness.StopServices(authPort, accountsPort)

	for _, user := range bank.Users()[:3] {
		if _, err := withdraw(ctx, user.CardNumber, "1234", 5_000); err != nil {
			log.Print(err)
			return
		}
	}

	fmt.Print(`
Three withdrawals, and the three services printed different trace ids for each.
Open devtools: each call started its own trace, so one withdrawal is now five
unrelated traces.
`)
}
