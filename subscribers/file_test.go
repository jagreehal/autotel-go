package subscribers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSubscriberWritesOneLinePerEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	sub, err := NewFileSubscriber(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"atm.cash_withdrawn", "atm.withdrawal_refused"} {
		if err := sub.Send(context.Background(), name, map[string]any{"trace_id": "abc", "withdrawal.amount_pence": 4000}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2:\n%s", len(lines), raw)
	}

	var event struct {
		Type       string         `json:"type"`
		Name       string         `json:"name"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "event" || event.Name != "atm.withdrawal_refused" || event.Attributes["trace_id"] != "abc" {
		t.Errorf("line decoded as %+v", event)
	}
}
