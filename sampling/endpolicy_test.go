package sampling

import "testing"

// Active decides whether Init pays for tail sampling at all. Getting it wrong in
// one direction silently drops the errors the policy promised to keep; in the
// other it records every span in the process for no benefit.
func TestEndPolicyActive(t *testing.T) {
	tests := []struct {
		name   string
		policy EndPolicy
		want   bool
	}{
		{
			name:   "defaults keep every error above a partial baseline",
			policy: EndPolicy{BaselineRate: 0.1, ErrorRate: 1.0, SlowThreshold: 1e9, SlowRate: 1.0},
			want:   true,
		},
		{
			name:   "keep everything needs no tail: the baseline already keeps it",
			policy: EndPolicy{BaselineRate: 1.0, ErrorRate: 1.0, SlowThreshold: 1e9, SlowRate: 1.0},
			want:   false,
		},
		{
			name:   "errors rarer than the baseline are decided at head",
			policy: EndPolicy{BaselineRate: 0.5, ErrorRate: 0.2, SlowRate: 0.1},
			want:   false,
		},
		{
			name:   "a slow rate alone is enough",
			policy: EndPolicy{BaselineRate: 0.1, ErrorRate: 0.1, SlowThreshold: 1e9, SlowRate: 1.0},
			want:   true,
		},
		{
			name:   "a slow rate with no threshold never fires",
			policy: EndPolicy{BaselineRate: 0.1, ErrorRate: 0.1, SlowRate: 1.0},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Active(); got != tt.want {
				t.Errorf("Active() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The baseline decision must come from the trace ID and nothing else, so that
// every span of a trace resolves it identically and routine traces arrive whole
// rather than as a scatter of surviving fragments.
func TestKeepAtRateIsDeterministicPerTrace(t *testing.T) {
	var traceID [16]byte
	traceID[15] = 42

	for i := 0; i < 100; i++ {
		if !keepAtRate(traceID, 0.5) {
			t.Fatal("the same trace ID produced different decisions at the same rate")
		}
	}

	kept := 0
	for i := 0; i < 256; i++ {
		var id [16]byte
		id[15] = byte(i)
		if keepAtRate(id, 0.25) {
			kept++
		}
	}
	if kept != 64 {
		t.Errorf("a 25%% rate kept %d/256 trace IDs, want 64", kept)
	}
}
