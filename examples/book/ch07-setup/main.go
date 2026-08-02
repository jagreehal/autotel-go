// Observability Engineering, chapter 7: instrumenting with OpenTelemetry.
//
// The book's companion repository sets this up by hand. Its file is 63 lines
// (1e/chapter-07-instrumentation-with-opentelemetry/src/tracing.go): a stdout
// exporter, a gRPC driver carrying TLS credentials, Honeycomb's endpoint and
// its two headers, a resource, a tracer provider, and a composite propagator —
// with five error checks and a log.Fatal on each.
//
// None of that is the chapter's subject. The chapter is about what to record;
// those 63 lines are the toll you pay before you can record anything.
//
// autotel-go produces the same wire configuration in one call. This program
// asserts that rather than claiming it: it applies the preset and checks the
// endpoint, headers, protocol and TLS against what the book's driver sets. It
// exits non-zero if they ever drift apart, so it runs in CI as evidence.
//
// No API key needed — nothing is exported. See the bottom for the real call.
package main

import (
	"fmt"
	"os"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/backends"
)

func main() {
	apiKey := envOr("HONEYCOMB_API_KEY", "hcaik_example")
	dataset := envOr("HONEYCOMB_DATASET", "fibonacci")

	// The whole of the book's tracing.go, minus the ceremony.
	cfg := autotel.DefaultConfig()
	backends.Honeycomb(backends.HoneycombConfig{
		APIKey:  apiKey,
		Dataset: dataset,
		Service: "fibonacci",
	})(cfg)

	if errs := cfg.OptionErrors(); len(errs) != 0 {
		fail("preset rejected its own configuration: %v", errs)
	}

	// Each check below is a line the book writes by hand.
	expect("endpoint", cfg.Endpoint, "api.honeycomb.io:443")            // otlpgrpc.WithEndpoint
	expect("x-honeycomb-team", cfg.Headers["x-honeycomb-team"], apiKey) // otlpgrpc.WithHeaders
	expect("x-honeycomb-dataset", cfg.Headers["x-honeycomb-dataset"], dataset)
	expect("protocol", string(cfg.Protocol), string(autotel.ProtocolGRPC)) // otlpgrpc.NewClient
	if cfg.Insecure {
		fail("TLS is off; the book passes credentials.NewClientTLSFromCert")
	}
	if cfg.ServiceName != "fibonacci" {
		fail("service name = %q; the book reads SERVICE_NAME into a resource", cfg.ServiceName)
	}

	fmt.Println("chapter 7 setup: 63 hand-written lines, or the six above.")
	fmt.Printf("  endpoint  %s (gRPC, TLS on)\n", cfg.Endpoint)
	fmt.Printf("  headers   x-honeycomb-team, x-honeycomb-dataset\n")
	fmt.Printf("  service   %s\n", cfg.ServiceName)

	// In a real service that is the entire setup, error handling included:
	//
	//	cleanup, err := autotel.Init(ctx, backends.Honeycomb(backends.HoneycombConfig{
	//	    APIKey: os.Getenv("HONEYCOMB_API_KEY"), Service: "fibonacci",
	//	}))
	//	if err != nil {
	//	    log.Fatal(err)
	//	}
	//	defer cleanup()
}

func expect(what, got, want string) {
	if got != want {
		fail("%s = %q, want %q", what, got, want)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
