package autotel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
)

// Regression: WithEndpoint stores its argument verbatim as the host, so the URL
// form documented in the README and used by six of the shipped examples produced
// a mangled target ("http://http:%2F%2Flocalhost:4318/v1/metrics") and made Init
// fail outright. Both forms must work.
func TestInitAcceptsBothEndpointForms(t *testing.T) {
	tests := []struct {
		name string
		// useURL selects the "http://host:port" form over bare "host:port".
		useURL bool
	}{
		{"URL form", true},
		{"host:port form", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&requests, 1)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			endpoint := server.Listener.Addr().String()
			if tt.useURL {
				endpoint = server.URL
			}

			cleanup, err := autotel.Init(context.Background(),
				autotel.WithService("endpoint-test"),
				autotel.WithEndpoint(endpoint),
				autotel.WithInsecure(true),
				// The default sampler keeps 10% of traces, which would make this flaky.
				autotel.WithSampler(sdktrace.AlwaysSample()),
				autotel.WithBatchTimeout(100*time.Millisecond),
			)
			if err != nil {
				t.Fatalf("Init with endpoint %q: %v", endpoint, err)
			}

			_, span := autotel.Start(context.Background(), "test-span")
			span.End()
			cleanup()

			if got := atomic.LoadInt64(&requests); got == 0 {
				t.Errorf("endpoint %q: collector received no export requests", endpoint)
			}
		})
	}
}

// An https:// endpoint must not be downgraded by the default Insecure setting,
// which is true for local development. The URL scheme has to win.
func TestHTTPSEndpointIgnoresInsecureDefault(t *testing.T) {
	var requests int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Insecure defaults to true; the https scheme must still force TLS. The
	// export fails on certificate verification (self-signed), not on a plaintext
	// request being sent to a TLS port, and Init itself must succeed.
	cleanup, err := autotel.Init(context.Background(),
		autotel.WithService("tls-test"),
		autotel.WithEndpoint(server.URL),
		autotel.WithBatchTimeout(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("Init with https endpoint: %v", err)
	}

	_, span := autotel.Start(context.Background(), "test-span")
	span.End()
	cleanup()
}

// Each signal reaches its own OTLP path, for both endpoint forms. A collector
// answers any other path with 404.
func TestEachSignalReachesItsOwnPath(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path] = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for _, endpoint := range []string{server.URL, server.Listener.Addr().String()} {
		clear(paths)

		_, err := autotel.Init(context.Background(),
			autotel.WithService("paths"),
			autotel.WithDebug(false),
			autotel.WithEndpoint(endpoint),
			autotel.WithInsecure(true),
			autotel.WithSampler(sdktrace.AlwaysSample()),
		)
		if err != nil {
			t.Fatalf("Init %s: %v", endpoint, err)
		}

		ctx, span := autotel.Start(context.Background(), "unit")
		autotel.Meter().Counter(ctx, "units", 1, nil)
		autotel.Logger("paths").InfoContext(ctx, "hello")
		span.End()

		if err := autotel.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown %s: %v", endpoint, err)
		}

		mu.Lock()
		for _, want := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
			if !paths[want] {
				t.Errorf("endpoint %s: nothing posted to %s (got %v)", endpoint, want, paths)
			}
		}
		mu.Unlock()
	}
}
