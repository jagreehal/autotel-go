// The auth service. Its own process, its own Init, its own service name.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/middleware"
	"github.com/jagreehal/autotel-go/v2/processors"
	"github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/harness"
	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

// cardBody carries the card number and PIN in the body rather than the URL,
// because HTTP instrumentation records the full URL on every span.
type cardBody struct {
	Card string `json:"card"`
	Pin  string `json:"pin"`
}

func last4(card string) string { return card[max(0, len(card)-4):] }

func card(w http.ResponseWriter, r *http.Request) {
	var body cardBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply, err := autotel.Trace(r.Context(), "auth.find_card", func(ctx context.Context, span autotel.Span) (harness.CardReply, error) {
		fmt.Printf("[auth-service]     trace %s\n", autotel.GetTraceID(ctx))
		span.SetAttribute("card.last4", last4(body.Card))

		user, err := bank.FindUserByCard(body.Card)
		return harness.CardReply{ID: user.ID, Name: user.Name}, err
	})
	harness.Reply(w, reply, err)
}

func verify(w http.ResponseWriter, r *http.Request) {
	var body cardBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply, err := autotel.Trace(r.Context(), "auth.verify_pin", func(_ context.Context, span autotel.Span) (string, error) {
		span.SetAttribute("card.last4", last4(body.Card))
		_, err := bank.VerifyPin(body.Card, body.Pin)
		return "ok", err
	})
	harness.Reply(w, reply, err)
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
		autotel.WithService("auth-service"),
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
	mux.HandleFunc("POST /card", card)
	mux.HandleFunc("POST /verify", verify)

	fmt.Printf("auth-service :%s\n", os.Args[1])
	if err := harness.Serve(os.Args[1], mux, withCallerContext); err != nil {
		log.Print(err)
	}
}
