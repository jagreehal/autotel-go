package autotel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func resolved(t *testing.T, opts ...Option) *Config {
	t.Helper()

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return resolveAndMergeConfig(cfg)
}

func TestWithDevtoolsDefaults(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("AUTOTEL_DEBUG", "")

	cfg := resolved(t, WithDevtools())

	if cfg.Endpoint != DevtoolsEndpoint || cfg.Protocol != ProtocolHTTP {
		t.Errorf("endpoint %q over %q, want %q over http", cfg.Endpoint, cfg.Protocol, DevtoolsEndpoint)
	}
	if !strings.Contains(cfg.Sampler.Description(), "AlwaysOn") {
		t.Errorf("sampler %s, want every trace kept", cfg.Sampler.Description())
	}
	if cfg.Debug == nil || *cfg.Debug {
		t.Errorf("debug printer %v, want off", cfg.Debug)
	}
}

// WithDevtools changes defaults only. Each of these was chosen explicitly and
// must survive it.
func TestWithDevtoolsLeavesExplicitChoicesAlone(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	cfg := resolved(t, WithDevtools(),
		WithEndpoint("collector:4318"),
		WithSampler(sdktrace.NeverSample()),
		WithDebug(true),
	)

	if cfg.Endpoint != "collector:4318" {
		t.Errorf("endpoint %q, want the explicit one", cfg.Endpoint)
	}
	if !strings.Contains(cfg.Sampler.Description(), "AlwaysOff") {
		t.Errorf("sampler %s, want the explicit one", cfg.Sampler.Description())
	}
	if cfg.Debug == nil || !*cfg.Debug {
		t.Error("debug turned off, want the explicit on")
	}

	adaptive := resolved(t, WithDevtools(), WithAdaptiveSampler())
	if strings.Contains(adaptive.Sampler.Description(), "AlwaysOn") {
		t.Error("an explicitly chosen adaptive sampler was replaced")
	}
}

// OTEL_EXPORTER_OTLP_ENDPOINT moves the target without a code change, and
// every span of the run arrives.
func TestWithDevtoolsFollowsTheEndpointVariable(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			requests.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)

	cleanup, err := Init(context.Background(), WithService("devtools-test"), WithDevtools(), WithMetrics(false), WithLogs(false))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer cleanup()

	for range 10 {
		_, span := Start(context.Background(), "unit")
		span.End()
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if requests.Load() == 0 {
		t.Fatal("no spans reached the receiver the variable names")
	}
}

// InitWithConfig callers set Sampler on the struct; WithDevtools keeps it.
func TestWithDevtoolsKeepsASamplerSetOnConfig(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	cfg := defaultConfig()
	cfg.Devtools = true
	cfg.Sampler = sdktrace.NeverSample()

	if got := resolveAndMergeConfig(cfg).Sampler.Description(); !strings.Contains(got, "AlwaysOff") {
		t.Errorf("sampler %s, want the one set on Config", got)
	}
}
