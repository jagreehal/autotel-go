package autotel

import (
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2/sampling"
)

// DevtoolsEndpoint is where WithDevtools sends telemetry: the OTLP/HTTP port
// autotel-devtools listens on (`npx autotel-devtools`).
const DevtoolsEndpoint = "http://127.0.0.1:4318"

// applyDevtools fills in the defaults WithDevtools promises, after every
// config layer has been merged so that an explicit choice is already visible.
func applyDevtools(cfg *Config) {
	if !cfg.Devtools {
		return
	}

	if cfg.Endpoint == "" {
		cfg.Endpoint = DevtoolsEndpoint
		cfg.Protocol = ProtocolHTTP
	}

	// InitWithConfig callers set Sampler directly; anything other than the
	// adaptive default counts as chosen.
	_, adaptive := cfg.Sampler.(*sampling.AdaptiveSampler)
	if !cfg.samplerChosen && (adaptive || cfg.Sampler == nil) {
		cfg.Sampler = trace.AlwaysSample()
		cfg.UseAdaptiveSampler = false
	}

	if cfg.Debug == nil && !envDebugSet() {
		off := false
		cfg.Debug = &off
	}
}
