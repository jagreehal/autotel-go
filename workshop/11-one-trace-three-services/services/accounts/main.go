// The accounts service. Owns the ledger, and the shard from part 10.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

func account(w http.ResponseWriter, r *http.Request) {
	found := autotel.TraceNoError(r.Context(), "accounts.get", func(ctx context.Context, span autotel.Span) bank.Account {
		fmt.Printf("[accounts-service] trace %s\n", autotel.GetTraceID(ctx))
		found := bank.GetAccount(r.URL.Query().Get("userId"))
		span.SetAttribute("account.id", found.ID)
		span.SetAttribute("account.shard", found.Shard)
		return found
	})
	harness.Reply(w, found, nil)
}

func debit(w http.ResponseWriter, r *http.Request) {
	amountPence, err := strconv.ParseInt(r.URL.Query().Get("amountPence"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	receipt, err := autotel.Trace(r.Context(), "accounts.debit", func(_ context.Context, span autotel.Span) (bank.Receipt, error) {
		found := bank.GetAccount(r.URL.Query().Get("userId"))
		span.SetAttribute("account.id", found.ID)
		span.SetAttribute("account.shard", found.Shard)
		span.SetAttribute("withdrawal.amount_pence", amountPence)
		return bank.Debit(found, amountPence)
	})
	harness.Reply(w, receipt, err)
}

func passThrough(next http.Handler) http.Handler { return next }

func main() {
	ctx := context.Background()
	if _, err := autotel.Init(ctx,
		autotel.WithService("accounts-service"),
		autotel.WithDevtools(),
	); err != nil {
		log.Fatal(err)
	}
	// Runs after Serve returns, which flushes this service's last spans.
	defer func() {
		if err := autotel.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /account", account)
	mux.HandleFunc("POST /debit", debit)

	fmt.Printf("accounts-service :%s\n", os.Args[1])
	if err := harness.Serve(os.Args[1], mux, passThrough); err != nil {
		log.Print(err)
	}
}
