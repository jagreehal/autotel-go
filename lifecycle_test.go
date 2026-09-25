package autotel_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
)

// Shutdown and a deferred cleanup both run in the common pattern; the second
// must not report the metric reader's "already shut down" as a failure.
func TestShutdownTwiceReportsTheFirstResult(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(), autotel.WithService("lifecycle"), autotel.WithDebug(false))
	if err != nil {
		t.Fatal(err)
	}

	if err := autotel.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := autotel.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	cleanup()
}

// With nothing listening, Shutdown returns within its deadline and says why.
func TestShutdownWithNoReceiverGivesUp(t *testing.T) {
	_, err := autotel.Init(context.Background(),
		autotel.WithService("lifecycle"),
		autotel.WithDebug(false),
		autotel.WithEndpoint("http://127.0.0.1:1"),
		autotel.WithSampler(sdktrace.AlwaysSample()),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, span := autotel.Start(context.Background(), "unsent")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err = autotel.Shutdown(ctx)
	if err == nil || !strings.Contains(err.Error(), "receiver listening") {
		t.Fatalf("err %v, want the no-receiver explanation", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %s", elapsed)
	}
}

// The IDs a caller prints are the ones a backend stores: 32 and 16 hex
// characters.
func TestGetTraceIDMatchesTheSpan(t *testing.T) {
	initWithExporter(t)

	ctx, span := autotel.Start(context.Background(), "ids")
	defer span.End()

	if got, want := autotel.GetTraceID(ctx), span.SpanContext().TraceID().String(); got != want {
		t.Errorf("GetTraceID = %q, want %q", got, want)
	}
	if got, want := autotel.GetSpanID(ctx), span.SpanContext().SpanID().String(); got != want {
		t.Errorf("GetSpanID = %q, want %q", got, want)
	}
}
