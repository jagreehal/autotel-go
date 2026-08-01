package backends_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/backends"
)

// The presets are only worth anything if the headers and endpoint they set
// actually reach the collector. Asserting on Config alone would not prove that:
// this library has twice shipped an option that set its field correctly and was
// then discarded downstream.
func TestPresetHeadersReachTheCollector(t *testing.T) {
	var (
		mu       sync.Mutex
		gotPath  string
		gotAuth  string
		requests int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// PostHog exercises the interesting parts: a bearer header and a path on the
	// endpoint, which only works through WithEndpointURL.
	cleanup, err := autotel.Init(context.Background(),
		backends.PostHog(backends.PostHogConfig{
			ProjectToken: "phc_secret",
			Service:      "checkout",
			Host:         server.URL,
		}),
		autotel.WithDebug(false),
		autotel.WithMetrics(false),
		// The default sampler keeps 10% of traces, which would make this flaky.
		autotel.WithSampler(sdktrace.AlwaysSample()),
		autotel.WithBatchTimeout(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, span := autotel.Start(context.Background(), "op")
	span.End()
	cleanup()

	mu.Lock()
	defer mu.Unlock()

	if requests == 0 {
		t.Fatal("the collector received no export requests")
	}
	if gotAuth != "Bearer phc_secret" {
		t.Errorf("Authorization header = %q, want the preset's bearer token", gotAuth)
	}
	if gotPath != "/i/v1/traces" {
		t.Errorf("request path = %q, want the preset path plus the OTLP traces path", gotPath)
	}
}

// A preset must not silently downgrade an https endpoint, since Insecure
// defaults to true for local development.
func TestPresetKeepsTLSForCloudEndpoints(t *testing.T) {
	cfg := autotel.DefaultConfig()
	backends.Honeycomb(backends.HoneycombConfig{APIKey: "k", Service: "s"})(cfg)

	if cfg.Insecure {
		t.Error("Honeycomb preset left Insecure set, which would send plaintext to a TLS endpoint")
	}
}
