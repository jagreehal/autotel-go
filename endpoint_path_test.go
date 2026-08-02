package autotel

import "testing"

// Config carries one Endpoint for every signal, which makes it the base endpoint
// in OTLP terms. WithEndpointURL treats the path as complete and appends nothing,
// so the signal path has to be joined on before it is handed over.
func TestSignalEndpointURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "no path falls through to the exporter default",
			base: "http://localhost:4318",
			want: "http://localhost:4318",
		},
		{
			name: "bare root falls through as well",
			base: "http://localhost:4318/",
			want: "http://localhost:4318/",
		},
		{
			name: "PostHog base path",
			base: "https://us.i.posthog.com/i",
			want: "https://us.i.posthog.com/i/v1/traces",
		},
		{
			name: "Langfuse base path",
			base: "https://cloud.langfuse.com/api/public/otel",
			want: "https://cloud.langfuse.com/api/public/otel/v1/traces",
		},
		{
			name: "Grafana gateway path",
			base: "https://otlp-gateway-prod.grafana.net/otlp",
			want: "https://otlp-gateway-prod.grafana.net/otlp/v1/traces",
		},
		{
			name: "trailing slash does not double up",
			base: "https://collector.internal/otlp/",
			want: "https://collector.internal/otlp/v1/traces",
		},
		{
			name: "an endpoint already naming the signal is left alone",
			base: "https://collector.internal/otlp/v1/traces",
			want: "https://collector.internal/otlp/v1/traces",
		},
		{
			name: "port is preserved",
			base: "https://collector.internal:4318/otlp",
			want: "https://collector.internal:4318/otlp/v1/traces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signalEndpointURL(tt.base, tracesPath); got != tt.want {
				t.Errorf("signalEndpointURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestSignalEndpointURLMetrics(t *testing.T) {
	got := signalEndpointURL("https://us.i.posthog.com/i", metricsPath)
	if want := "https://us.i.posthog.com/i/v1/metrics"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEndpointIsURL(t *testing.T) {
	urls := []string{"http://localhost:4318", "https://api.honeycomb.io"}
	hostPorts := []string{"localhost:4318", "api.honeycomb.io:443", ""}

	for _, endpoint := range urls {
		if !endpointIsURL(endpoint) {
			t.Errorf("%q should be recognised as a URL", endpoint)
		}
	}
	for _, endpoint := range hostPorts {
		if endpointIsURL(endpoint) {
			t.Errorf("%q should be recognised as host:port", endpoint)
		}
	}
}
