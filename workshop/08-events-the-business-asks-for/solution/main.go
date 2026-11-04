// check-count: 8 name = "atm.withdraw"
// check-count: 7 withdrawal.result = "dispensed"
// check-stdout: 8 business events written to
// check-stdout: atm.withdrawal_refused
// check-stdout: atm.cash_withdrawn

// Part 8: Our solution: Track alongside the span, not instead of it
//
//	go run ./workshop/08-events-the-business-asks-for/solution
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/subscribers"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const eventsFile = "workshop/08-events-the-business-asks-for/events.ndjson"

func withdraw(ctx context.Context, branch, cardNumber, pin string, amountPence int64) (bank.Receipt, error) {
	return autotel.Trace(ctx, "atm.withdraw", func(ctx context.Context, _ autotel.Span) (receipt bank.Receipt, err error) {
		record := autotel.NewRequestLogger(ctx)
		record.Set(map[string]any{
			"atm":        map[string]any{"branch": branch},
			"withdrawal": map[string]any{"amount_pence": amountPence},
		})

		// Declared here so the refusal event can name the customer too. Without
		// it, "do refused customers come back?" has nothing to join on.
		var userID string

		defer func() {
			var refused *bank.Error
			if errors.As(err, &refused) {
				code := string(refused.Code)
				record.Set(map[string]any{"withdrawal": map[string]any{"result": "refused", "refusal_code": code}})

				properties := map[string]any{
					"atm.branch":              branch,
					"withdrawal.amount_pence": amountPence,
					"withdrawal.refusal_code": code,
				}
				if userID != "" {
					properties["user.id"] = userID
				}
				autotel.Track(ctx, "atm.withdrawal_refused", properties)
			}
			record.EmitNow()
		}()

		user, err := autotel.Trace(ctx, "bank.find_user_by_card", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.FindUserByCard(cardNumber)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		userID = user.ID
		record.Set(map[string]any{"user": map[string]any{"id": user.ID}})

		if _, err := autotel.Trace(ctx, "bank.verify_pin", func(context.Context, autotel.Span) (bank.User, error) {
			return bank.VerifyPin(cardNumber, pin)
		}); err != nil {
			return bank.Receipt{}, err
		}

		account := autotel.TraceNoError(ctx, "bank.get_account", func(context.Context, autotel.Span) bank.Account {
			return bank.GetAccount(user.ID)
		})
		receipt, err = autotel.Trace(ctx, "bank.debit", func(context.Context, autotel.Span) (bank.Receipt, error) {
			return bank.Debit(account, amountPence)
		})
		if err != nil {
			return bank.Receipt{}, err
		}
		record.Set(map[string]any{"withdrawal": map[string]any{"result": "dispensed"}})

		// The business event. Named for what happened in the world, not for what
		// the code did, and carrying the numbers a product person would group by.
		autotel.Track(ctx, "atm.cash_withdrawn", map[string]any{
			"user.id":                     user.ID,
			"atm.branch":                  branch,
			"withdrawal.amount_pence":     amountPence,
			"account.balance_after_pence": receipt.BalancePence,
		})

		return receipt, nil
	})
}

func attempt(ctx context.Context, index int, branch string, amountPence int64) {
	// Refused; both records above already say why.
	_, _ = withdraw(ctx, branch, bank.Users()[index].CardNumber, "1234", amountPence)
}

type event struct {
	Name       string         `json:"name"`
	Attributes map[string]any `json:"attributes"`
}

func readEvents() ([]event, error) {
	file, err := os.Open(eventsFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var events []event
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		var e event
		if err := json.Unmarshal(lines.Bytes(), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, lines.Err()
}

func main() {
	ctx := context.Background()

	// Start each run with an empty file; the subscriber appends.
	_ = os.Remove(eventsFile)

	// Where business events go. In production this is PostHog, Mixpanel,
	// Amplitude or your own webhook; the Track calls do not change.
	file, err := subscribers.NewFileSubscriber(eventsFile)
	if err != nil {
		log.Fatal(err)
	}

	if _, err := autotel.Init(ctx,
		autotel.WithService("atm"),
		autotel.WithDevtools(),
		autotel.WithSubscribers(file),
	); err != nil {
		log.Fatal(err)
	}

	bank.ResetBalances()

	for index := range 6 {
		branch := "bridge-street"
		if index%2 == 1 {
			branch = "kings-cross"
		}
		attempt(ctx, index, branch, 10_000)
	}

	attempt(ctx, 0, "bridge-street", 25_000)
	attempt(ctx, 0, "bridge-street", 15_000)

	// Track queues events and a background worker delivers them, so the file is
	// complete only once Shutdown has drained the queue.
	if err := autotel.Shutdown(ctx); err != nil {
		log.Print(err)
	}

	events, err := readEvents()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n%d business events written to events.ndjson\n\n", len(events))

	refusedTrace := ""
	for _, e := range events {
		pence, _ := e.Attributes["withdrawal.amount_pence"].(float64)
		traceID, _ := e.Attributes["trace_id"].(string)
		fmt.Printf("  %-28s £%7.2f  trace %s\n", e.Name, pence/100, traceID[:min(len(traceID), 12)])
		if e.Name == "atm.withdrawal_refused" {
			refusedTrace = traceID
		}
	}

	fmt.Printf(`
Every event carries the trace_id of the request that produced it. Paste this one
into the devtools search box and you land on the exact withdrawal behind the
number:

  trace_id = "%s"
`, refusedTrace)
}
