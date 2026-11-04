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
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/processors"
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

// withCallerContext runs each request inside the context the caller sent, so any
// span started below is a child of the caller's span rather than a root.
func withCallerContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incoming := middleware.ExtractHeaders(r.Context(), r)
		next.ServeHTTP(w, r.WithContext(incoming))
	})
}

func main() {
	ctx := context.Background()
	if _, err := autotel.Init(ctx,
		autotel.WithService("accounts-service"),
		autotel.WithDevtools(),
		// Copy any baggage the caller sent onto every span this service creates,
		// with no prefix. Nothing sets baggage until part 12; this is the
		// receiver half, and it costs nothing while the header is empty.
		autotel.WithBaggageAttributes(processors.WithBaggagePrefix("")),
		autotel.WithDebugCapture(),
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
	if err := harness.Serve(os.Args[1], mux, withCallerContext); err != nil {
		log.Print(err)
	}
}
