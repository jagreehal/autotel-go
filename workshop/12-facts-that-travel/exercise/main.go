// Part 12: Facts that travel
//
// One trace, three services. Part 10's method works again, right up to the moment
// you try to group the accounts service by branch.
//
// The ATM knows which machine and which branch. The accounts service does not, and
// no function signature between them carries it.
//
// TODO: make atm.branch and atm.id available on every span in every service,
// without adding a parameter to anything.
//
//	go run ./workshop/12-facts-that-travel/exercise
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const (
	authPort     = 7301
	accountsPort = 7302
)

var (
	authURL     = fmt.Sprintf("http://127.0.0.1:%d", authPort)
	accountsURL = fmt.Sprintf("http://127.0.0.1:%d", accountsPort)
)

type machine struct{ id, branch string }

var machines = []machine{
	{"atm_0431", "bridge-street"},
	{"atm_1330", "canada-water"},
}

func withdraw(ctx context.Context, m machine, cardNumber string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (bank.Receipt, error) {
		record := autotel.NewRequestLogger(ctx)
		defer record.EmitNow()

		// These two facts exist here and nowhere else.
		autotel.SetAttributes(ctx, map[string]any{"atm.id": m.id, "atm.branch": m.branch})

		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})

		request := harness.NewRequest(ctx, http.MethodPost, authURL+"/card", map[string]string{"card": cardNumber})
		middleware.InjectHeaders(ctx, request)
		var user harness.CardReply
		if err := harness.Send(request, &user); err != nil {
			return bank.Receipt{}, err
		}

		request = harness.NewRequest(ctx, http.MethodPost, authURL+"/verify", map[string]string{"card": cardNumber, "pin": "1234"})
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

		request = harness.NewRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/debit?userId=%s&amountPence=%d", accountsURL, user.ID, amountPence), nil)
		middleware.InjectHeaders(ctx, request)
		var receipt bank.Receipt
		if err := harness.Send(request, &receipt); err != nil {
			return bank.Receipt{}, err
		}

		record.Set(map[string]any{
			"account":    map[string]any{"id": account.ID},
			"withdrawal": map[string]any{"result": "dispensed"},
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

	if err := harness.StartServices("services-propagated", authPort, accountsPort); err != nil {
		log.Print(err)
		return
	}
	defer harness.StopServices(authPort, accountsPort)

	for index, user := range bank.Users()[:6] {
		if _, err := withdraw(ctx, machines[index%2], user.CardNumber, 5_000); err != nil {
			log.Print(err)
			return
		}
	}

	fmt.Print(`
6 withdrawals. Now try this in devtools:

  service = accounts-service and atm.branch = "canada-water"

Nothing. The accounts service has never heard of a branch.
`)
}
