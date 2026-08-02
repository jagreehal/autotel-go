package webhook_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2/webhook"
)

// newTestTracer installs an in-memory tracer provider as the global provider,
// which is what ParkingLot uses internally for callback spans.
func newTestTracer(t *testing.T) (trace.Tracer, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	return provider.Tracer("test"), recorder
}

func TestInMemoryStore_SaveLoadDelete(t *testing.T) {
	store := webhook.NewInMemoryStore()
	defer store.Close()

	ctx := context.Background()
	sc := &webhook.StoredContext{TraceID: "abc", ParkedAt: time.Now(), TTL: time.Hour}

	if err := store.Save(ctx, "k", sc); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Load(ctx, "k")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.TraceID != "abc" {
		t.Fatalf("expected the stored context back, got %+v", got)
	}

	exists, err := store.Exists(ctx, "k")
	if err != nil || !exists {
		t.Errorf("expected the key to exist, got exists=%v err=%v", exists, err)
	}

	if err := store.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := store.Load(ctx, "k"); got != nil {
		t.Error("expected nil after delete")
	}
}

func TestInMemoryStore_ExpiredEntriesAreNotReturned(t *testing.T) {
	store := webhook.NewInMemoryStore()
	defer store.Close()

	ctx := context.Background()
	expired := &webhook.StoredContext{
		TraceID:  "abc",
		ParkedAt: time.Now().Add(-2 * time.Hour),
		TTL:      time.Hour,
	}
	if err := store.Save(ctx, "k", expired); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Load(ctx, "k")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Error("expected an expired context to be treated as absent")
	}

	exists, _ := store.Exists(ctx, "k")
	if exists {
		t.Error("expected Exists to report false for an expired context")
	}
}

func TestStoredContext_ZeroTTLNeverExpires(t *testing.T) {
	sc := &webhook.StoredContext{ParkedAt: time.Now().Add(-100 * time.Hour)}
	if sc.IsExpired() {
		t.Error("expected a zero TTL to mean no expiry")
	}
}

func TestInMemoryStore_CloseIsIdempotent(t *testing.T) {
	store := webhook.NewInMemoryStore()
	store.Close()
	store.Close() // must not panic on a second call
}

func TestParkingLot_ParkAndRetrieveRoundTrip(t *testing.T) {
	tracer, _ := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store, webhook.WithDefaultTTL(time.Hour))

	ctx, span := tracer.Start(context.Background(), "initiate-payment")
	originalTraceID := span.SpanContext().TraceID().String()

	if err := lot.Park(ctx, "payment:1", webhook.WithMetadata(map[string]string{"order_id": "1"})); err != nil {
		t.Fatalf("park: %v", err)
	}
	span.End()

	parked, err := lot.Retrieve(context.Background(), "payment:1")
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if parked == nil {
		t.Fatal("expected to retrieve the parked context")
	}
	if parked.TraceID != originalTraceID {
		t.Errorf("expected trace ID %s, got %s", originalTraceID, parked.TraceID)
	}
	if parked.Metadata["order_id"] != "1" {
		t.Errorf("expected metadata to round-trip, got %v", parked.Metadata)
	}
}

func TestParkingLot_AutoDeleteRemovesAfterRetrieve(t *testing.T) {
	tracer, _ := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store, webhook.WithAutoDelete(true))

	ctx, span := tracer.Start(context.Background(), "initiate")
	if err := lot.Park(ctx, "k"); err != nil {
		t.Fatalf("park: %v", err)
	}
	span.End()

	if _, err := lot.Retrieve(context.Background(), "k"); err != nil {
		t.Fatalf("first retrieve: %v", err)
	}

	second, err := lot.Retrieve(context.Background(), "k")
	if err != nil {
		t.Fatalf("second retrieve: %v", err)
	}
	if second != nil {
		t.Error("expected the context to be deleted after the first retrieve")
	}
}

func TestParkingLot_RetrieveMissInvokesCallback(t *testing.T) {
	store := webhook.NewInMemoryStore()
	defer store.Close()

	var missed string
	lot := webhook.NewParkingLot(store, webhook.WithOnMiss(func(key string) { missed = key }))

	if _, err := lot.Retrieve(context.Background(), "absent"); err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if missed != "absent" {
		t.Errorf("expected the miss callback to fire with the key, got %q", missed)
	}
}

func TestParkingLot_RetrieveAndTraceLinksToParkedSpan(t *testing.T) {
	tracer, recorder := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store)

	ctx, span := tracer.Start(context.Background(), "initiate-payment")
	originalTraceID := span.SpanContext().TraceID()
	if err := lot.Park(ctx, "payment:1"); err != nil {
		t.Fatalf("park: %v", err)
	}
	span.End()

	_, callbackSpan, parked := lot.RetrieveAndTrace(context.Background(), "payment:1", "stripe.webhook")
	callbackSpan.End()

	if parked == nil {
		t.Fatal("expected the parked context to be returned")
	}

	var callback sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "stripe.webhook" {
			callback = s
		}
	}
	if callback == nil {
		t.Fatal("expected the callback span to be recorded")
	}

	links := callback.Links()
	if len(links) != 1 {
		t.Fatalf("expected exactly one link to the parked span, got %d", len(links))
	}
	if links[0].SpanContext.TraceID() != originalTraceID {
		t.Errorf("expected the link to point at trace %s, got %s",
			originalTraceID, links[0].SpanContext.TraceID())
	}
}

func TestParkingLot_RetrieveAndTraceOnMissStillReturnsUsableSpan(t *testing.T) {
	_, recorder := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store)

	ctx, span, parked := lot.RetrieveAndTrace(context.Background(), "absent", "stripe.webhook")
	span.End()

	if parked != nil {
		t.Error("expected no parked context")
	}
	if ctx == nil || span == nil {
		t.Fatal("expected a usable context and span even on a miss")
	}
	_ = recorder
}

// failingStore returns an error from every Load, standing in for an unavailable backend.
type failingStore struct{ webhook.Store }

func (failingStore) Save(context.Context, string, *webhook.StoredContext) error { return nil }
func (failingStore) Load(context.Context, string) (*webhook.StoredContext, error) {
	return nil, errors.New("redis unavailable")
}
func (failingStore) Delete(context.Context, string) error         { return nil }
func (failingStore) Exists(context.Context, string) (bool, error) { return false, nil }

func TestParkingLot_RetrieveAndTraceSurvivesStoreFailure(t *testing.T) {
	lot := webhook.NewParkingLot(failingStore{})

	// A backend outage must not drop the callback: the span is still created.
	ctx, span, parked := lot.RetrieveAndTrace(context.Background(), "k", "stripe.webhook")
	span.End()

	if parked != nil {
		t.Error("expected no parked context when the store fails")
	}
	if ctx == nil || span == nil {
		t.Fatal("expected a usable context and span despite the store failure")
	}
}

func TestParkingLot_ExistsAndDelete(t *testing.T) {
	tracer, _ := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store)

	ctx, span := tracer.Start(context.Background(), "initiate")
	if err := lot.Park(ctx, "k"); err != nil {
		t.Fatalf("park: %v", err)
	}
	span.End()

	exists, err := lot.Exists(context.Background(), "k")
	if err != nil || !exists {
		t.Errorf("expected the parked key to exist, got exists=%v err=%v", exists, err)
	}

	if err := lot.Delete(context.Background(), "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if exists, _ := lot.Exists(context.Background(), "k"); exists {
		t.Error("expected the key to be gone after delete")
	}
}

func TestParkingLot_KeyPrefixIsApplied(t *testing.T) {
	tracer, _ := newTestTracer(t)
	store := webhook.NewInMemoryStore()
	defer store.Close()

	lot := webhook.NewParkingLot(store, webhook.WithKeyPrefix("tenant-a:"))

	ctx, span := tracer.Start(context.Background(), "initiate")
	if err := lot.Park(ctx, "k"); err != nil {
		t.Fatalf("park: %v", err)
	}
	span.End()

	// The prefix must be applied on the way into the store...
	if raw, _ := store.Load(context.Background(), "tenant-a:k"); raw == nil {
		t.Error("expected the stored key to carry the prefix")
	}
	// ...and transparently on the way out.
	if got, _ := lot.Retrieve(context.Background(), "k"); got == nil {
		t.Error("expected retrieval by the unprefixed key to succeed")
	}
}

func TestCreateCorrelationKey(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{"payment"}, "payment"},
		{[]string{"payment", "order-123", "stripe"}, "payment:order-123:stripe"},
	}

	for _, tc := range cases {
		if got := webhook.CreateCorrelationKey(tc.parts...); got != tc.want {
			t.Errorf("CreateCorrelationKey(%v) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

func TestInMemoryStore_ConcurrentAccessIsRaceFree(t *testing.T) {
	store := webhook.NewInMemoryStore(webhook.WithCleanupInterval(time.Millisecond))
	defer store.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			key := string(rune('a' + i%26))
			_ = store.Save(ctx, key, &webhook.StoredContext{ParkedAt: time.Now(), TTL: time.Minute})
			_, _ = store.Load(ctx, key)
			_, _ = store.Exists(ctx, key)
			_ = store.Size()
		}(i)
	}
	wg.Wait()
}
