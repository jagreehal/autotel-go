package autotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jagreehal/autotel-go/v2"
)

func TestRequestLoggerBuildsOneWideEvent(t *testing.T) {
	exporter := initWithExporter(t)

	_, _ = autotel.Trace(context.Background(), "atm.withdraw", func(ctx context.Context, _ autotel.Span) (int, error) {
		log := autotel.NewRequestLogger(ctx)
		log.Set(map[string]any{"atm": map[string]any{"branch": "bridge-street"}})
		log.Set(map[string]any{"withdrawal.amount_pence": 4000, "user": map[string]any{"id": nil}})

		snapshot := log.EmitNow()
		if snapshot.TraceID == "" || snapshot.Fields["atm.branch"] != "bridge-street" {
			t.Errorf("snapshot %+v", snapshot)
		}

		log.Set(map[string]any{"late": true})
		if again := log.EmitNow(); again.Timestamp != snapshot.Timestamp {
			t.Error("a second EmitNow sent a second record")
		}

		return 0, nil
	})

	attrs := attrsOf(t, exporter, "atm.withdraw")
	if attrs["atm.branch"] != "bridge-street" || attrs["withdrawal.amount_pence"] != "4000" {
		t.Errorf("fields missing from the span: %v", attrs)
	}
	if _, ok := attrs["user.id"]; ok {
		t.Error("a nil field was recorded")
	}
	if _, ok := attrs["late"]; ok {
		t.Error("a field set after EmitNow reached the span")
	}
	if attrs["event:log.emit.manual"] != "2" {
		t.Errorf("emit event carried %s fields, want 2", attrs["event:log.emit.manual"])
	}
}

// With no span there is nothing to record, and nothing may panic.
func TestRequestLoggerWithoutASpan(t *testing.T) {
	log := autotel.NewRequestLogger(context.Background())
	log.Set(map[string]any{"a": 1})
	log.Info("hello", nil)
	log.Error(nil, nil)
	if snapshot := log.EmitNow(); snapshot.TraceID != "" || snapshot.Fields["a"] != 1 {
		t.Errorf("snapshot %+v", snapshot)
	}
}

// After EmitNow the record is sealed, and an error arriving late changes
// nothing on the span.
func TestRequestLoggerErrorAfterEmitIsIgnored(t *testing.T) {
	exporter := initWithExporter(t)

	_, _ = autotel.Trace(context.Background(), "sealed", func(ctx context.Context, _ autotel.Span) (int, error) {
		event := autotel.NewRequestLogger(ctx)
		event.EmitNow()
		event.Error(errors.New("late"), nil)

		return 0, nil
	})

	if status := attrsOf(t, exporter, "sealed")["status"]; status == "Error" {
		t.Error("an error after EmitNow marked the span failed")
	}
}

type plan string

// A named string type records as the string, so a query for plan = premium
// matches it.
func TestRequestLoggerRecordsNamedTypesAsTheirBase(t *testing.T) {
	exporter := initWithExporter(t)

	_, _ = autotel.Trace(context.Background(), "named", func(ctx context.Context, _ autotel.Span) (int, error) {
		autotel.NewRequestLogger(ctx).Set(map[string]any{"plan": plan("premium")})
		return 0, nil
	})

	if got := attrsOf(t, exporter, "named")["plan"]; got != "premium" {
		t.Errorf("plan = %q, want premium", got)
	}
}
