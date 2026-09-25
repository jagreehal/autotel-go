package autotel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jagreehal/autotel-go/v2"
)

func attrsOf(t *testing.T, exporter *tracetest.InMemoryExporter, name string) map[string]string {
	t.Helper()

	if err := autotel.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, span := range exporter.GetSpans() {
		if span.Name == name {
			out := map[string]string{"status": span.Status.Code.String()}
			for _, kv := range span.Attributes {
				out[string(kv.Key)] = kv.Value.String()
			}
			for _, event := range span.Events {
				out["event:"+event.Name] = fmt.Sprint(len(event.Attributes))
			}
			return out
		}
	}
	t.Fatalf("no span named %s", name)
	return nil
}

func TestTraceRecordsAStructuredErrorsFields(t *testing.T) {
	exporter := initWithExporter(t)
	cause := errors.New("ledger said no")

	_, err := autotel.Trace(context.Background(), "withdraw", func(ctx context.Context, _ autotel.Span) (int, error) {
		return 0, fmt.Errorf("wrapped: %w", &autotel.StructuredError{
			Message: "Daily limit reached",
			Why:     "Over the £300 limit.",
			Fix:     "Try tomorrow.",
			Code:    "DAILY_LIMIT_EXCEEDED",
			Status:  429,
			Details: map[string]any{"limit": map[string]any{"pence": 30000}},
			Cause:   cause,
		})
	})

	if !errors.Is(err, cause) {
		t.Error("the cause is not reachable through errors.Is")
	}

	attrs := attrsOf(t, exporter, "withdraw")
	want := map[string]string{
		"status":                    codes.Error.String(),
		"error.why":                 "Over the £300 limit.",
		"error.fix":                 "Try tomorrow.",
		"error.code":                "DAILY_LIMIT_EXCEEDED",
		"error.status":              "429",
		"error.details.limit.pence": "30000",
	}
	for key, value := range want {
		if attrs[key] != value {
			t.Errorf("%s = %q, want %q", key, attrs[key], value)
		}
	}
}

func TestParseError(t *testing.T) {
	parsed := autotel.ParseError(fmt.Errorf("at the boundary: %w", &autotel.StructuredError{
		Message: "Card retained", Fix: "Call us.", Code: "CARD_RETAINED", Status: 423,
	}))
	if parsed.Message != "Card retained" || parsed.Status != 423 || parsed.Fix != "Call us." || parsed.Code != "CARD_RETAINED" {
		t.Errorf("parsed %+v", parsed)
	}

	plain := autotel.ParseError(errors.New("boom"))
	if plain.Message != "boom" || plain.Status != 500 {
		t.Errorf("plain error parsed as %+v, want boom/500", plain)
	}

	if unset := autotel.ParseError(&autotel.StructuredError{Message: "x"}); unset.Status != 500 {
		t.Errorf("unset status parsed as %d, want 500", unset.Status)
	}
}

// Internal is for the backend. It must never reach a client through JSON.
func TestStructuredErrorJSONOmitsInternal(t *testing.T) {
	raw, err := json.Marshal(&autotel.StructuredError{
		Message:  "Card retained",
		Why:      "Three wrong PINs.",
		Internal: map[string]any{"card.number": "4000123412341234"},
		Cause:    errors.New("attempts=3"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "4000123412341234") {
		t.Fatalf("internal context leaked: %s", raw)
	}
	if !strings.Contains(string(raw), `"why":"Three wrong PINs."`) {
		t.Errorf("why missing: %s", raw)
	}
}

func TestStructuredErrorFormats(t *testing.T) {
	err := &autotel.StructuredError{Message: "Card retained", Fix: "Call us.", Status: 423}

	if got := fmt.Sprint(err); got != "Card retained" {
		t.Errorf("%%v = %q", got)
	}
	if got := fmt.Sprintf("%+v", err); !strings.Contains(got, "Fix: Call us.") || !strings.Contains(got, "Status: 423") {
		t.Errorf("%%+v = %q", got)
	}
}
