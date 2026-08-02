package backends_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/backends"
)

// apply runs a preset against a default config and returns the result.
func apply(opt autotel.Option) *autotel.Config {
	cfg := autotel.DefaultConfig()
	opt(cfg)
	return cfg
}

// applyErr runs a preset and returns the single validation error it raised.
func applyErr(t *testing.T, opt autotel.Option) string {
	t.Helper()

	errs := apply(opt).OptionErrors()
	if len(errs) != 1 {
		t.Fatalf("expected exactly one validation error, got %d: %v", len(errs), errs)
	}
	return errs[0].Error()
}

func TestHoneycomb(t *testing.T) {
	cfg := apply(backends.Honeycomb(backends.HoneycombConfig{
		APIKey:      "hcaik_test",
		Service:     "checkout",
		Dataset:     "production",
		Environment: "production",
		Version:     "1.2.3",
		SampleRate:  10,
	}))

	if errs := cfg.OptionErrors(); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if cfg.Endpoint != "api.honeycomb.io:443" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Protocol != autotel.ProtocolGRPC {
		t.Errorf("protocol = %q, want grpc", cfg.Protocol)
	}
	if cfg.Insecure {
		t.Error("a cloud endpoint must not be insecure")
	}
	if cfg.ServiceName != "checkout" || cfg.Environment != "production" || cfg.ServiceVersion != "1.2.3" {
		t.Errorf("identity not applied: %+v", cfg)
	}

	want := map[string]string{
		"x-honeycomb-team":    "hcaik_test",
		"x-honeycomb-dataset": "production",
	}
	for k, v := range want {
		if cfg.Headers[k] != v {
			t.Errorf("header %s = %q, want %q", k, cfg.Headers[k], v)
		}
	}
	if got := cfg.ResourceAttributes["SampleRate"]; got != "10" {
		t.Errorf("SampleRate resource attribute = %q, want %q", got, "10")
	}
	if cfg.UseAdaptiveSampler {
		t.Error("Honeycomb sampling must replace the adaptive sampler")
	}
	if _, ok := cfg.Headers["x-honeycomb-samplerate"]; ok {
		t.Error("the Events API sample-rate header must not be used for OTLP")
	}
}

func TestHoneycombSampleRateControlsHeadSampling(t *testing.T) {
	cfg := apply(backends.Honeycomb(backends.HoneycombConfig{
		APIKey: "k", Service: "s", SampleRate: 1,
	}))

	result := cfg.Sampler.ShouldSample(trace.SamplingParameters{
		TraceID: oteltrace.TraceID{1},
	})
	if result.Decision != trace.RecordAndSample {
		t.Errorf("SampleRate 1 decision = %v, want RecordAndSample", result.Decision)
	}
}

func TestHoneycombRejectsNegativeSampleRate(t *testing.T) {
	msg := applyErr(t, backends.Honeycomb(backends.HoneycombConfig{
		APIKey: "k", Service: "s", SampleRate: -1,
	}))
	if !strings.Contains(msg, "sample rate") {
		t.Errorf("got %q", msg)
	}
}

func TestHoneycombOmitsOptionalHeaders(t *testing.T) {
	cfg := apply(backends.Honeycomb(backends.HoneycombConfig{APIKey: "k", Service: "s"}))

	if _, ok := cfg.Headers["x-honeycomb-dataset"]; ok {
		t.Error("dataset header must be omitted when no dataset is set")
	}
	if _, ok := cfg.Headers["x-honeycomb-samplerate"]; ok {
		t.Error("sample-rate header must be omitted when no rate is set")
	}
}

func TestHoneycombRequiresAPIKey(t *testing.T) {
	msg := applyErr(t, backends.Honeycomb(backends.HoneycombConfig{Service: "s"}))
	if !strings.Contains(msg, "API key is required") {
		t.Errorf("error should explain the missing key, got %q", msg)
	}
}

func TestDatadogDirectIngestion(t *testing.T) {
	cfg := apply(backends.Datadog(backends.DatadogConfig{
		APIKey:  "dd_test",
		Service: "checkout",
		Site:    backends.DatadogEU,
	}))

	if cfg.Endpoint != "https://otlp.datadoghq.eu" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Headers["dd-api-key"] != "dd_test" {
		t.Errorf("dd-api-key = %q", cfg.Headers["dd-api-key"])
	}
}

func TestDatadogDefaultsToUS1(t *testing.T) {
	cfg := apply(backends.Datadog(backends.DatadogConfig{APIKey: "k", Service: "s"}))

	if cfg.Endpoint != "https://otlp.datadoghq.com" {
		t.Errorf("endpoint = %q, want the US1 site", cfg.Endpoint)
	}
}

func TestDatadogAgentModeNeedsNoAPIKey(t *testing.T) {
	cfg := apply(backends.Datadog(backends.DatadogConfig{
		Service:  "checkout",
		UseAgent: true,
	}))

	if errs := cfg.OptionErrors(); len(errs) != 0 {
		t.Fatalf("agent mode must not require an API key: %v", errs)
	}
	if cfg.Endpoint != "http://localhost:4318" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	// The Agent authenticates itself; leaking an API key here would be wrong.
	if _, ok := cfg.Headers["dd-api-key"]; ok {
		t.Error("agent mode must not set an API key header")
	}
}

func TestDatadogAgentHostAndPort(t *testing.T) {
	cfg := apply(backends.Datadog(backends.DatadogConfig{
		Service:   "s",
		UseAgent:  true,
		AgentHost: "dd-agent",
		AgentPort: 4317,
	}))

	if cfg.Endpoint != "http://dd-agent:4317" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
}

func TestDatadogRequiresAPIKeyForDirect(t *testing.T) {
	msg := applyErr(t, backends.Datadog(backends.DatadogConfig{Service: "s"}))
	if !strings.Contains(msg, "UseAgent") {
		t.Errorf("error should point at the agent alternative, got %q", msg)
	}
}

func TestGrafana(t *testing.T) {
	cfg := apply(backends.Grafana(backends.GrafanaConfig{
		Endpoint: "https://otlp-gateway-prod-eu-west-0.grafana.net/otlp",
		Headers:  map[string]string{"Authorization": "Basic abc123"},
		Service:  "checkout",
	}))

	if cfg.Endpoint != "https://otlp-gateway-prod-eu-west-0.grafana.net/otlp" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Headers["Authorization"] != "Basic abc123" {
		t.Errorf("auth header = %q", cfg.Headers["Authorization"])
	}
	if !cfg.MetricsEnabled {
		t.Error("Grafana preset should enable metrics")
	}
	if cfg.Insecure {
		t.Error("an https endpoint must not be insecure")
	}
}

func TestGrafanaRequiresEndpoint(t *testing.T) {
	msg := applyErr(t, backends.Grafana(backends.GrafanaConfig{Service: "s"}))
	if !strings.Contains(msg, "endpoint is required") {
		t.Errorf("got %q", msg)
	}
}

func TestParseHeaders(t *testing.T) {
	// The form Grafana's console hands out, including a percent-encoded space.
	got := backends.ParseHeaders("Authorization=Basic%20abc123,X-Scope-OrgID=42")

	if got["Authorization"] != "Basic abc123" {
		t.Errorf("Authorization = %q, want the %%20 decoded", got["Authorization"])
	}
	if got["X-Scope-OrgID"] != "42" {
		t.Errorf("X-Scope-OrgID = %q", got["X-Scope-OrgID"])
	}
}

func TestParseHeadersIgnoresMalformedPairs(t *testing.T) {
	got := backends.ParseHeaders("novalue,,=orphan,good=yes")

	if len(got) != 1 || got["good"] != "yes" {
		t.Errorf("expected only the well-formed pair, got %v", got)
	}
}

func TestLogfire(t *testing.T) {
	cfg := apply(backends.Logfire(backends.LogfireConfig{
		WriteToken: "pylf_test",
		Service:    "checkout",
		Region:     backends.LogfireEU,
	}))

	if cfg.Endpoint != "https://logfire-eu.pydantic.dev" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Headers["Authorization"] != "pylf_test" {
		t.Errorf("Authorization = %q", cfg.Headers["Authorization"])
	}
}

func TestLogfireRequiresRegion(t *testing.T) {
	// A Logfire token is region-specific, so defaulting would silently ship data
	// to the wrong continent.
	msg := applyErr(t, backends.Logfire(backends.LogfireConfig{
		WriteToken: "t",
		Service:    "s",
	}))
	if !strings.Contains(msg, "region is required") {
		t.Errorf("got %q", msg)
	}
}

func TestLogfireRejectsUnknownRegion(t *testing.T) {
	msg := applyErr(t, backends.Logfire(backends.LogfireConfig{
		WriteToken: "t",
		Service:    "s",
		Region:     backends.LogfireRegion("apac"),
	}))
	if !strings.Contains(msg, "unknown Logfire region") {
		t.Errorf("got %q", msg)
	}
}

func TestLogfireSelfHostedEndpointDoesNotRequireCloudRegion(t *testing.T) {
	cfg := apply(backends.Logfire(backends.LogfireConfig{
		WriteToken: "t",
		Service:    "s",
		Endpoint:   "https://logfire.internal",
	}))

	if errs := cfg.OptionErrors(); len(errs) != 0 {
		t.Fatalf("self-hosted endpoint must not require a cloud region: %v", errs)
	}
	if cfg.Endpoint != "https://logfire.internal" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
}

func TestLangfuse(t *testing.T) {
	cfg := apply(backends.Langfuse(backends.LangfuseConfig{
		PublicKey: "pk-lf-1",
		SecretKey: "sk-lf-2",
		Service:   "checkout",
	}))

	if cfg.Endpoint != "https://cloud.langfuse.com/api/public/otel" {
		t.Errorf("endpoint = %q, want the EU default with the OTLP path", cfg.Endpoint)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-lf-1:sk-lf-2"))
	if cfg.Headers["Authorization"] != want {
		t.Errorf("Authorization = %q, want %q", cfg.Headers["Authorization"], want)
	}
}

func TestLangfuseUSRegion(t *testing.T) {
	cfg := apply(backends.Langfuse(backends.LangfuseConfig{
		PublicKey: "pk", SecretKey: "sk", Service: "s", Region: backends.LangfuseUS,
	}))

	if cfg.Endpoint != "https://us.cloud.langfuse.com/api/public/otel" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
}

func TestLangfuseBaseURLTrailingSlash(t *testing.T) {
	cfg := apply(backends.Langfuse(backends.LangfuseConfig{
		PublicKey: "pk", SecretKey: "sk", Service: "s",
		BaseURL: "https://langfuse.internal/",
	}))

	if cfg.Endpoint != "https://langfuse.internal/api/public/otel" {
		t.Errorf("endpoint = %q, want no doubled slash", cfg.Endpoint)
	}
}

func TestLangfuseRequiresBothKeys(t *testing.T) {
	if msg := applyErr(t, backends.Langfuse(backends.LangfuseConfig{
		SecretKey: "sk", Service: "s",
	})); !strings.Contains(msg, "public key is required") {
		t.Errorf("got %q", msg)
	}
	if msg := applyErr(t, backends.Langfuse(backends.LangfuseConfig{
		PublicKey: "pk", Service: "s",
	})); !strings.Contains(msg, "secret key is required") {
		t.Errorf("got %q", msg)
	}
}

func TestPostHog(t *testing.T) {
	cfg := apply(backends.PostHog(backends.PostHogConfig{
		ProjectToken: "phc_test",
		Service:      "checkout",
	}))

	if cfg.Endpoint != "https://us.i.posthog.com/i" {
		t.Errorf("endpoint = %q, want the US default with the OTLP path", cfg.Endpoint)
	}
	if cfg.Headers["Authorization"] != "Bearer phc_test" {
		t.Errorf("Authorization = %q", cfg.Headers["Authorization"])
	}
}

func TestPostHogEURegion(t *testing.T) {
	cfg := apply(backends.PostHog(backends.PostHogConfig{
		ProjectToken: "t", Service: "s", Region: backends.PostHogEU,
	}))

	if cfg.Endpoint != "https://eu.i.posthog.com/i" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
}

func TestPostHogDoesNotDoubleThePath(t *testing.T) {
	cfg := apply(backends.PostHog(backends.PostHogConfig{
		ProjectToken: "t", Service: "s", Host: "https://ph.internal/i",
	}))

	if cfg.Endpoint != "https://ph.internal/i" {
		t.Errorf("endpoint = %q, want the path applied once", cfg.Endpoint)
	}
}

func TestCollectorDefaults(t *testing.T) {
	cfg := apply(backends.Collector(backends.CollectorConfig{Service: "checkout"}))

	if cfg.Endpoint != "http://localhost:4318" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Protocol != autotel.ProtocolHTTP {
		t.Errorf("protocol = %q", cfg.Protocol)
	}
	// Local development stays plaintext.
	if !cfg.Insecure {
		t.Error("a local http collector should stay insecure")
	}
}

func TestCollectorHTTPSTurnsOffInsecure(t *testing.T) {
	cfg := apply(backends.Collector(backends.CollectorConfig{
		Service:  "s",
		Endpoint: "https://collector.internal:4318",
	}))

	if cfg.Insecure {
		t.Error("an https collector must not be insecure")
	}
}

func TestCollectorGRPCUsesGRPCDefaultPort(t *testing.T) {
	cfg := apply(backends.Collector(backends.CollectorConfig{
		Service:  "s",
		Protocol: autotel.ProtocolGRPC,
	}))

	if cfg.Endpoint != "http://localhost:4317" {
		t.Errorf("endpoint = %q, want the OTLP/gRPC default", cfg.Endpoint)
	}
}

// Every preset must report a missing service name rather than exporting as
// "unknown-service", which is invisible in a vendor UI.
func TestAllPresetsRequireServiceName(t *testing.T) {
	presets := map[string]autotel.Option{
		"Honeycomb": backends.Honeycomb(backends.HoneycombConfig{APIKey: "k"}),
		"Datadog":   backends.Datadog(backends.DatadogConfig{APIKey: "k"}),
		"Grafana":   backends.Grafana(backends.GrafanaConfig{Endpoint: "https://e"}),
		"Logfire":   backends.Logfire(backends.LogfireConfig{WriteToken: "t", Region: backends.LogfireUS}),
		"Langfuse":  backends.Langfuse(backends.LangfuseConfig{PublicKey: "p", SecretKey: "s"}),
		"PostHog":   backends.PostHog(backends.PostHogConfig{ProjectToken: "t"}),
		"Collector": backends.Collector(backends.CollectorConfig{}),
	}

	for name, opt := range presets {
		t.Run(name, func(t *testing.T) {
			msg := applyErr(t, opt)
			if !strings.Contains(msg, "service name") {
				t.Errorf("error should name the missing service, got %q", msg)
			}
		})
	}
}

// A preset validation failure must surface from Init rather than being ignored.
func TestInitReportsPresetValidationFailure(t *testing.T) {
	cleanup, err := autotel.Init(context.Background(), backends.Honeycomb(backends.HoneycombConfig{
		Service: "checkout", // no API key
	}))
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("expected Init to fail when a preset is misconfigured")
	}
	if !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("Init error should carry the preset message, got %q", err)
	}
}
