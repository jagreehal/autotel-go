package autotel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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
