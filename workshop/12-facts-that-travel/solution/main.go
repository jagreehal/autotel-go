// check-count: 6 name = "atm.withdraw"
// check-count: 3 name = "accounts.debit" and service = accounts-service and atm.branch = "canada-water"
// check: service = auth-service and atm.id = "atm_0431"
// check: service = accounts-service and autotel.debug = "part-10"
// check: service = auth-service and autotel.debug = "part-10"

// Part 12: Our solution: put the fact in baggage, once
//
//	go run ./workshop/12-facts-that-travel/solution
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const (
	authPort     = 7401
	accountsPort = 7402
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

// withBaggage returns ctx with each key and value in baggage. Baggage lives in
// the context, so like every context change it applies to what you pass ctx to.
func withBaggage(ctx context.Context, pairs ...string) (context.Context, error) {
	for index := 0; index < len(pairs); index += 2 {
		var err error
		if ctx, err = autotel.SetBaggage(ctx, pairs[index], pairs[index+1]); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func withdraw(ctx context.Context, m machine, cardNumber string, amountPence int64, debugLabel string) (http.Header, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (http.Header, error) {
		record := autotel.NewRequestLogger(ctx)
		defer record.EmitNow()

		// Baggage, not an attribute. An attribute stays on this span; baggage
		// rides the baggage header to every service this request reaches, and
		// WithBaggageAttributes in each Init copies it onto their spans too.
		pairs := []string{"atm.id", m.id, "atm.branch", m.branch}

		// Full fidelity for one request, decided per request rather than per deploy.
		if debugLabel != "" {
			pairs = append(pairs, autotel.DebugBaggageKey, debugLabel)
		}

		ctx, err := withBaggage(ctx, pairs...)
		if err != nil {
			return nil, err
		}

		record.Set(map[string]any{"withdrawal": map[string]any{"amount_pence": amountPence}})

		// InjectHeaders writes traceparent and baggage together.
		request := harness.NewRequest(ctx, http.MethodPost, authURL+"/card", map[string]string{"card": cardNumber})
		middleware.InjectHeaders(ctx, request)
		var user harness.CardReply
		if err := harness.Send(request, &user); err != nil {
			return nil, err
		}

		request = harness.NewRequest(ctx, http.MethodPost, authURL+"/verify", map[string]string{"card": cardNumber, "pin": "1234"})
		middleware.InjectHeaders(ctx, request)
		if err := harness.Send(request, nil); err != nil {
			return nil, err
		}

		request = harness.NewRequest(ctx, http.MethodGet, accountsURL+"/account?userId="+user.ID, nil)
		middleware.InjectHeaders(ctx, request)
		var account bank.Account
		if err := harness.Send(request, &account); err != nil {
			return nil, err
		}

		request = harness.NewRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/debit?userId=%s&amountPence=%d", accountsURL, user.ID, amountPence), nil)
		middleware.InjectHeaders(ctx, request)
		if err := harness.Send(request, nil); err != nil {
			return nil, err
		}

		record.Set(map[string]any{
			"account":    map[string]any{"id": account.ID},
			"withdrawal": map[string]any{"result": "dispensed"},
		})

		return request.Header, nil
	})
}

func main() {
	ctx := context.Background()
	if _, err := autotel.Init(ctx,
		autotel.WithService("atm"),
		autotel.WithDevtools(),
		// Copy baggage onto spans with no prefix, the same as the two services do.
		autotel.WithBaggageAttributes(processors.WithBaggagePrefix("")),
		autotel.WithDebugCapture(),
	); err != nil {
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

	var lastHeaders http.Header
	for index, user := range bank.Users()[:6] {
		// One withdrawal is marked for full fidelity. In production this comes
		// from a gateway, a feature flag, or a curl with an extra header.
		debugLabel := ""
		if index == 3 {
			debugLabel = "part-10"
		}

		headers, err := withdraw(ctx, machines[index%2], user.CardNumber, 5_000, debugLabel)
		if err != nil {
			log.Print(err)
			return
		}
		lastHeaders = headers
	}

	fmt.Printf(`
The headers on the last hop:

  traceparent: %s
  baggage:     %s

Now the query the exercise could not answer:

  service = accounts-service and atm.branch = "canada-water"

And the withdrawal that asked to be kept whatever the sampler decides:

  autotel.debug = "part-10"
`, lastHeaders.Get("traceparent"), lastHeaders.Get("baggage"))
}
