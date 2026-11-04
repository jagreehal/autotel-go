// check-count: 3 name = "atm.withdraw"
// check-count: 3 name = "accounts.debit" and service = accounts-service
// check: name = "auth.verify_pin" and service = auth-service
// check-one-trace: atm, auth-service, accounts-service
// check-child: accounts.debit of atm.withdraw
// check-child: auth.find_card of atm.withdraw

// Part 11: Our solution: inject on the way out, extract on the way in
//
//	go run ./workshop/11-one-trace-three-services/solution
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const (
	authPort     = 7201
	accountsPort = 7202
)

var (
	authURL     = fmt.Sprintf("http://127.0.0.1:%d", authPort)
	accountsURL = fmt.Sprintf("http://127.0.0.1:%d", accountsPort)
	showHeader  sync.Once
)

func withdraw(ctx context.Context, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		record := autotel.NewRequestLogger(ctx)
		defer record.EmitNow()
		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})
		fmt.Printf("[atm]              trace %s\n", autotel.GetTraceID(ctx))

		// middleware.InjectHeaders writes the W3C traceparent header (and
		// baggage, in part 12) describing the span in ctx.
		request := harness.NewRequest(ctx, http.MethodPost, authURL+"/card", map[string]string{"card": cardNumber})
		middleware.InjectHeaders(ctx, request)
		showHeader.Do(func() {
			fmt.Printf("                   traceparent: %s\n", request.Header.Get("traceparent"))
		})

		var user harness.CardReply
		if err := harness.Send(request, &user); err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"user": map[string]any{"id": user.ID}})

		request = harness.NewRequest(ctx, http.MethodPost, authURL+"/verify", map[string]string{"card": cardNumber, "pin": pin})
		middleware.InjectHeaders(ctx, request)
		if err := harness.Send(request, nil); err != nil {
			return bank.Receipt{}, err
		}

		request = harness.NewRequest(ctx, http.MethodGet, accountsURL+"/account?userId="+user.ID, nil)
		middleware.InjectHeaders(ctx, request)
		var account bank.Account
		if err := harness.Send(request, &account); err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"account": map[string]any{"id": account.ID, "shard": account.Shard}})

		request = harness.NewRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/debit?userId=%s&amountPence=%d", accountsURL, user.ID, amountPence), nil)
		middleware.InjectHeaders(ctx, request)
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

	if err := harness.StartServices("services-propagated", authPort, accountsPort); err != nil {
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
Three services print the same trace id per withdrawal. In devtools, open one
trace: atm.withdraw on top, and auth and accounts spans nested underneath, all
in one waterfall.

  name = "atm.withdraw"
`)
}
