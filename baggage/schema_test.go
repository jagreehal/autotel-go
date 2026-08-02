package baggage_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	otelbaggage "go.opentelemetry.io/otel/baggage"

	"github.com/jagreehal/autotel-go/v2/baggage"
)

func TestSchema_ValidateEnum(t *testing.T) {
	schema := baggage.NewSchema().DefineEnumField("priority", []string{"low", "high"})

	if err := schema.Validate("priority", "high"); err != nil {
		t.Errorf("expected 'high' to be valid, got %v", err)
	}
	if err := schema.Validate("priority", "urgent"); err == nil {
		t.Error("expected 'urgent' to be rejected")
	}
}

func TestSchema_ValidateNumberEnforcesZeroBound(t *testing.T) {
	// Regression: a bound of 0 used to be indistinguishable from "unset",
	// so the lower bound was silently never enforced.
	schema := baggage.NewSchema().DefineNumberField("discount", 0, 100)

	if err := schema.Validate("discount", "-1"); err == nil {
		t.Error("expected -1 to be rejected by a minimum of 0")
	}
	if err := schema.Validate("discount", "0"); err != nil {
		t.Errorf("expected 0 to be accepted, got %v", err)
	}
	if err := schema.Validate("discount", "101"); err == nil {
		t.Error("expected 101 to be rejected by a maximum of 100")
	}
	if err := schema.Validate("discount", "banana"); err == nil {
		t.Error("expected a non-numeric value to be rejected")
	}
}

func TestSchema_ValidateStringMaxLengthAndPattern(t *testing.T) {
	schema := baggage.NewSchema().DefineField("region", baggage.FieldConstraint{
		Type:      baggage.FieldTypeString,
		MaxLength: 10,
		Pattern:   `^[a-z]{2}-[a-z]+-\d$`,
	})

	if err := schema.Validate("region", "us-west-2"); err != nil {
		t.Errorf("expected a well-formed region to pass, got %v", err)
	}
	if err := schema.Validate("region", "US_WEST_2"); err == nil {
		t.Error("expected a value violating the pattern to be rejected")
	}
	if err := schema.Validate("region", "aa-bbbbbbbbbbbbbb-1"); err == nil {
		t.Error("expected an over-length value to be rejected")
	}
}

func TestSchema_ValidateRequiredAndStrictMode(t *testing.T) {
	schema := baggage.NewSchema(baggage.WithStrictMode(true)).
		DefineStringField("tenant_id", 64, true)

	if err := schema.Validate("tenant_id", ""); err == nil {
		t.Error("expected an empty required field to be rejected")
	}
	if err := schema.Validate("unknown_field", "x"); err == nil {
		t.Error("expected an unknown field to be rejected in strict mode")
	}

	lenient := baggage.NewSchema().DefineStringField("tenant_id", 64, true)
	if err := lenient.Validate("unknown_field", "x"); err != nil {
		t.Errorf("expected unknown fields to be allowed outside strict mode, got %v", err)
	}
}

func TestSchema_ProcessHashesAndRedacts(t *testing.T) {
	schema := baggage.NewSchema().
		DefineHashedField("user_id").
		DefinePIIField("note")

	hashed, err := schema.Process("user_id", "alice@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hashed == "alice@example.com" {
		t.Error("expected the user_id value to be hashed")
	}
	if len(hashed) != 16 {
		t.Errorf("expected a 16-char hash, got %d chars", len(hashed))
	}

	redacted, err := schema.Process("note", "contact bob@example.com now")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(redacted, "bob@example.com") {
		t.Errorf("expected the email to be redacted, got %q", redacted)
	}
}

func TestSchema_DetectPII(t *testing.T) {
	schema := baggage.NewSchema()

	detected := schema.DetectPII("mail me at a@b.co or call 555-123-4567")
	if len(detected) == 0 {
		t.Fatal("expected PII to be detected")
	}

	found := make(map[string]bool)
	for _, name := range detected {
		found[name] = true
	}
	if !found["email"] {
		t.Errorf("expected an email detection, got %v", detected)
	}
}

func TestSafeBaggage_SetAndGet(t *testing.T) {
	sb := baggage.NewSafeBaggage(baggage.DefaultSchema())

	ctx, err := sb.Set(context.Background(), baggage.KeyTenantID, "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sb.Get(ctx, baggage.KeyTenantID); got != "acme" {
		t.Errorf("expected 'acme', got %q", got)
	}
}

func TestSafeBaggage_SizeLimitIsPerContextNotPerInstance(t *testing.T) {
	// Regression: the size accumulator was per-SafeBaggage and never reset, so a
	// long-lived instance started rejecting every write once cumulative bytes
	// crossed the limit, regardless of what the context actually carried.
	schema := baggage.NewSchema(baggage.WithMaxTotalSize(64)).
		DefineStringField("tenant_id", 64, false)
	sb := baggage.NewSafeBaggage(schema)

	for i := 0; i < 50; i++ {
		ctx, err := sb.Set(context.Background(), "tenant_id", "acme")
		if err != nil {
			t.Fatalf("iteration %d: fresh context should always fit, got %v", i, err)
		}
		if got := sb.Get(ctx, "tenant_id"); got != "acme" {
			t.Fatalf("iteration %d: expected value to be set, got %q", i, got)
		}
	}
}

func TestSafeBaggage_SizeLimitRejectsOversizedContext(t *testing.T) {
	schema := baggage.NewSchema(baggage.WithMaxTotalSize(30)).
		DefineStringField("a", 64, false).
		DefineStringField("b", 64, false)
	sb := baggage.NewSafeBaggage(schema)

	ctx, err := sb.Set(context.Background(), "a", strings.Repeat("x", 20))
	if err != nil {
		t.Fatalf("expected the first value to fit, got %v", err)
	}
	if _, err := sb.Set(ctx, "b", strings.Repeat("y", 20)); err == nil {
		t.Error("expected the second value to exceed the total size limit")
	}
}

func TestSafeBaggage_CardinalityLimit(t *testing.T) {
	schema := baggage.NewSchema(baggage.WithCardinalityLimit("route", 2)).
		DefineStringField("route", 64, false)
	sb := baggage.NewSafeBaggage(schema)

	for _, v := range []string{"/a", "/b"} {
		if _, err := sb.Set(context.Background(), "route", v); err != nil {
			t.Fatalf("expected %q to be within the cardinality limit, got %v", v, err)
		}
	}
	// Repeating a known value must not consume budget.
	if _, err := sb.Set(context.Background(), "route", "/a"); err != nil {
		t.Errorf("expected a repeated value to be allowed, got %v", err)
	}
	if _, err := sb.Set(context.Background(), "route", "/c"); err == nil {
		t.Error("expected a third distinct value to exceed the cardinality limit")
	}
}

func TestSafeBaggage_ConcurrentSetIsRaceFree(t *testing.T) {
	schema := baggage.NewSchema(baggage.WithCardinalityLimit("tenant_id", 1000)).
		DefineStringField("tenant_id", 64, false)
	sb := baggage.NewSafeBaggage(schema)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = sb.Set(context.Background(), "tenant_id", string(rune('a'+i%26)))
		}(i)
	}
	wg.Wait()
}

func TestSafeBaggage_CheckRequiredFields(t *testing.T) {
	schema := baggage.NewSchema().
		DefineStringField("tenant_id", 64, true).
		DefineStringField("region", 32, true)
	sb := baggage.NewSafeBaggage(schema)

	ctx, err := sb.Set(context.Background(), "tenant_id", "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	missing := sb.CheckRequiredFields(ctx)
	if len(missing) != 1 || missing[0] != "region" {
		t.Errorf("expected only 'region' to be missing, got %v", missing)
	}
}

func TestSafeBaggage_GetAll(t *testing.T) {
	sb := baggage.NewSafeBaggage(baggage.DefaultSchema())

	ctx, err := sb.SetMultiple(context.Background(), map[string]string{
		baggage.KeyTenantID: "acme",
		baggage.KeyRegion:   "us-west-2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	all := sb.GetAll(ctx)
	if all[baggage.KeyTenantID] != "acme" || all[baggage.KeyRegion] != "us-west-2" {
		t.Errorf("expected both values to round-trip, got %v", all)
	}
}

func TestSafeBaggage_RejectsInvalidValueWithoutMutatingContext(t *testing.T) {
	schema := baggage.NewSchema().DefineEnumField("priority", []string{"low", "high"})
	sb := baggage.NewSafeBaggage(schema)

	ctx, err := sb.Set(context.Background(), "priority", "urgent")
	if err == nil {
		t.Fatal("expected an invalid enum value to be rejected")
	}
	if len(otelbaggage.FromContext(ctx).Members()) != 0 {
		t.Error("expected the context to be unchanged when validation fails")
	}
}
