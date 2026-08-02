package autotel_test

import (
	"testing"
	"time"

	"github.com/jagreehal/autotel-go/v2"
)

// These assertions are weaker than the ones in pipeline_e2e_test.go, and the
// difference matters. They check that an option populates the config, not that
// anything downstream reads it, which is the exact gap that let WithSpanFilter
// and the adaptive sampler's error rate ship doing nothing.
//
// They are here because the effects cannot be seen through an in-memory span
// exporter: an exporter built in memory has no endpoint, no protocol and no
// headers, and the event pipeline never touches a span. Where an end-to-end
// assertion becomes possible, it should replace the one below.

func applyOptions(opts ...autotel.Option) *autotel.Config {
	cfg := autotel.DefaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

func TestTransportOptionsPopulateConfig(t *testing.T) {
	cfg := applyOptions(
		autotel.WithProtocol(autotel.ProtocolGRPC),
		autotel.WithHeaders(map[string]string{"x-api-key": "secret"}),
		autotel.WithBackend("honeycomb"),
	)

	if cfg.Protocol != autotel.ProtocolGRPC {
		t.Errorf("Protocol = %q, want grpc", cfg.Protocol)
	}
	if cfg.Headers["x-api-key"] != "secret" {
		t.Errorf("Headers[x-api-key] = %q, want secret", cfg.Headers["x-api-key"])
	}
	if cfg.BackendPreset != "honeycomb" {
		t.Errorf("BackendPreset = %q, want honeycomb", cfg.BackendPreset)
	}
}

// WithHeaders merges rather than replacing, so two calls do not lose the first
// set of credentials.
func TestWithHeadersMergesAcrossCalls(t *testing.T) {
	cfg := applyOptions(
		autotel.WithHeaders(map[string]string{"first": "1"}),
		autotel.WithHeaders(map[string]string{"second": "2"}),
	)

	for key, want := range map[string]string{"first": "1", "second": "2"} {
		if cfg.Headers[key] != want {
			t.Errorf("Headers[%s] = %q, want %q", key, cfg.Headers[key], want)
		}
	}
}

func TestEventQueueOptionsPopulateConfig(t *testing.T) {
	cfg := applyOptions(
		autotel.WithEventQueue(500, 2*time.Second, 7),
		autotel.WithEventBackoff(10*time.Millisecond, time.Second, 30*time.Second),
		autotel.WithEventRetry(3, 5*time.Millisecond),
	)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"EventQueueSize", cfg.EventQueueSize, 500},
		{"EventFlushInterval", cfg.EventFlushInterval, 2 * time.Second},
		{"EventCBThreshold", cfg.EventCBThreshold, 7},
		{"EventBackoffMin", cfg.EventBackoffMin, 10 * time.Millisecond},
		{"EventBackoffMax", cfg.EventBackoffMax, time.Second},
		{"EventCBReset", cfg.EventCBReset, 30 * time.Second},
		{"EventMaxRetries", cfg.EventMaxRetries, 3},
		{"EventJitter", cfg.EventJitter, 5 * time.Millisecond},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}
