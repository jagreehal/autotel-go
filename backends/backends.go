// Package backends provides vendor presets for autotel.
//
// A preset is an [autotel.Option] that sets the endpoint, protocol, TLS mode and
// authentication headers a given observability vendor expects. It configures
// *where* telemetry goes; it never changes what you instrument.
//
//	cleanup, err := autotel.Init(ctx, backends.Honeycomb(backends.HoneycombConfig{
//	    APIKey:  os.Getenv("HONEYCOMB_API_KEY"),
//	    Service: "checkout",
//	}))
//
// Presets stay OTLP-first: everything here is a plain OTLP exporter pointed at a
// vendor's ingest endpoint, so nothing is locked in and you can always drop back
// to autotel.WithEndpoint and autotel.WithHeaders.
//
// Missing or malformed credentials are reported through Init rather than
// panicking, so a typo fails at startup instead of exporting into the void.
package backends

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2"
)

// invalid returns an Option that makes Init fail with the given message.
func invalid(format string, args ...any) autotel.Option {
	return func(c *autotel.Config) {
		c.OptionError(fmt.Errorf(format, args...))
	}
}

// compose flattens several options into one so a preset can return a single value.
func compose(opts ...autotel.Option) autotel.Option {
	return func(c *autotel.Config) {
		for _, opt := range opts {
			opt(c)
		}
	}
}

// identity applies the service, environment and version fields shared by every preset.
func identity(service, environment, version string) autotel.Option {
	opts := []autotel.Option{autotel.WithService(service)}
	if environment != "" {
		opts = append(opts, autotel.WithEnvironment(environment))
	}
	if version != "" {
		opts = append(opts, autotel.WithServiceVersion(version))
	}
	return compose(opts...)
}

// trimTrailingSlash normalises a base URL so joining a path cannot double up separators.
func trimTrailingSlash(url string) string {
	return strings.TrimRight(url, "/")
}

// --- Honeycomb ---------------------------------------------------------------

// HoneycombConfig configures the Honeycomb preset.
type HoneycombConfig struct {
	// APIKey is the Honeycomb ingest key (required). Found under Account → API Keys.
	APIKey string
	// Service names the service; Honeycomb routes modern environments on it (required).
	Service string
	// Dataset targets a specific dataset. Only classic accounts need this; modern
	// environments route on service name.
	Dataset string
	// Environment is the deployment environment, e.g. "production".
	Environment string
	// Version is the service version, for deployment tracking.
	Version string
	// Endpoint overrides the ingest endpoint, for EU or on-premises installs.
	// Defaults to api.honeycomb.io:443.
	Endpoint string
	// SampleRate applies Honeycomb head sampling: 1 keeps everything, 10 keeps 1 in 10.
	// Leave zero to rely on autotel's own sampling.
	SampleRate int
}

// Honeycomb configures export to Honeycomb over gRPC.
func Honeycomb(cfg HoneycombConfig) autotel.Option {
	if cfg.APIKey == "" {
		return invalid("backends: Honeycomb API key is required; create one under Account → API Keys")
	}
	if cfg.Service == "" {
		return invalid("backends: Honeycomb requires a service name")
	}
	if cfg.SampleRate < 0 {
		return invalid("backends: Honeycomb sample rate cannot be negative")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "api.honeycomb.io:443"
	}

	headers := map[string]string{"x-honeycomb-team": cfg.APIKey}
	if cfg.Dataset != "" {
		headers["x-honeycomb-dataset"] = cfg.Dataset
	}
	opts := []autotel.Option{
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(autotel.ProtocolGRPC),
		autotel.WithEndpoint(endpoint),
		autotel.WithInsecure(false),
		autotel.WithHeaders(headers),
	}
	if cfg.SampleRate > 0 {
		opts = append(opts, honeycombSampling(cfg.SampleRate))
	}

	return compose(opts...)
}

// honeycombSampling configures real SDK head sampling and tells Honeycomb how
// to reweight the sampled spans. x-honeycomb-samplerate is an Events API header
// and does not control an OTLP SDK sampler.
func honeycombSampling(sampleRate int) autotel.Option {
	return func(c *autotel.Config) {
		if c.ResourceAttributes == nil {
			c.ResourceAttributes = make(map[string]string)
		}
		c.ResourceAttributes["SampleRate"] = strconv.Itoa(sampleRate)
		c.Sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1 / float64(sampleRate)))
		c.UseAdaptiveSampler = false
	}
}

// --- Datadog -----------------------------------------------------------------

// DatadogSite identifies a Datadog regional site.
type DatadogSite string

// Datadog regional sites.
const (
	DatadogUS1    DatadogSite = "datadoghq.com"
	DatadogEU     DatadogSite = "datadoghq.eu"
	DatadogUS3    DatadogSite = "us3.datadoghq.com"
	DatadogUS5    DatadogSite = "us5.datadoghq.com"
	DatadogAP1    DatadogSite = "ap1.datadoghq.com"
	DatadogUS1FED DatadogSite = "ddog-gov.com"
)

// DatadogConfig configures the Datadog preset.
type DatadogConfig struct {
	// APIKey is required for direct cloud ingestion, and unused with UseAgent.
	APIKey string
	// Site selects the regional endpoint. Defaults to DatadogUS1.
	Site DatadogSite
	// Service names the service (required).
	Service string
	// Environment is the deployment environment, e.g. "production".
	Environment string
	// Version is the service version.
	Version string
	// UseAgent sends to a local Datadog Agent instead of the cloud, which handles
	// authentication itself, so no API key is needed.
	UseAgent bool
	// AgentHost is the Agent host when UseAgent is set. Defaults to localhost.
	AgentHost string
	// AgentPort is the Agent OTLP port when UseAgent is set. Defaults to 4318.
	AgentPort int
}

// Datadog configures export to Datadog, either directly or via a local Agent.
func Datadog(cfg DatadogConfig) autotel.Option {
	if cfg.Service == "" {
		return invalid("backends: Datadog requires a service name")
	}

	if cfg.UseAgent {
		host := cfg.AgentHost
		if host == "" {
			host = "localhost"
		}
		port := cfg.AgentPort
		if port == 0 {
			port = 4318
		}
		// The Agent authenticates on the service's behalf, so no headers here.
		return compose(
			identity(cfg.Service, cfg.Environment, cfg.Version),
			autotel.WithProtocol(autotel.ProtocolHTTP),
			autotel.WithEndpoint(fmt.Sprintf("http://%s:%d", host, port)),
		)
	}

	if cfg.APIKey == "" {
		return invalid("backends: Datadog API key is required for direct ingestion; " +
			"set APIKey, or set UseAgent to send through a local Datadog Agent")
	}

	site := cfg.Site
	if site == "" {
		site = DatadogUS1
	}

	return compose(
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(autotel.ProtocolHTTP),
		autotel.WithEndpoint(fmt.Sprintf("https://otlp.%s", site)),
		autotel.WithHeaders(map[string]string{"dd-api-key": cfg.APIKey}),
	)
}

// --- Grafana Cloud -----------------------------------------------------------

// GrafanaConfig configures the Grafana Cloud preset.
type GrafanaConfig struct {
	// Endpoint is the OTLP gateway URL (required). Find it under
	// Grafana Cloud → your stack → Connections → OpenTelemetry → Configure.
	Endpoint string
	// Headers carries authentication. Grafana's UI hands out an
	// "Authorization=Basic ..." style string; use ParseHeaders for that form.
	Headers map[string]string
	// Service names the service (required).
	Service string
	// Environment is the deployment environment.
	Environment string
	// Version is the service version.
	Version string
}

// Grafana configures export to Grafana Cloud.
func Grafana(cfg GrafanaConfig) autotel.Option {
	if cfg.Endpoint == "" {
		return invalid("backends: Grafana Cloud endpoint is required; find it under " +
			"Grafana Cloud → your stack → Connections → OpenTelemetry → Configure")
	}
	if cfg.Service == "" {
		return invalid("backends: Grafana requires a service name")
	}

	opts := []autotel.Option{
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithEndpoint(cfg.Endpoint),
		autotel.WithMetrics(true),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, autotel.WithHeaders(cfg.Headers))
	}
	if !strings.HasPrefix(cfg.Endpoint, "http://") {
		opts = append(opts, autotel.WithInsecure(false))
	}

	return compose(opts...)
}

// ParseHeaders converts the "key=value,key2=value2" header string that several
// vendor consoles hand out into the map the presets take. %20 is decoded so a
// copied "Authorization=Basic%20abc123" works as-is.
func ParseHeaders(raw string) map[string]string {
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.ReplaceAll(strings.TrimSpace(value), "%20", " ")
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

// --- Pydantic Logfire --------------------------------------------------------

// LogfireRegion identifies a Logfire data region.
type LogfireRegion string

// Logfire data regions.
const (
	LogfireUS LogfireRegion = "us"
	LogfireEU LogfireRegion = "eu"
)

var logfireEndpoints = map[LogfireRegion]string{
	LogfireUS: "https://logfire-us.pydantic.dev",
	LogfireEU: "https://logfire-eu.pydantic.dev",
}

// LogfireConfig configures the Pydantic Logfire preset.
type LogfireConfig struct {
	// WriteToken is the Logfire write token (required), from Project Settings → Write Tokens.
	WriteToken string
	// Service names the service (required).
	Service string
	// Region is required unless Endpoint is set: a token issued in one region is
	// not valid in the other, and guessing silently sends data to the wrong continent.
	Region LogfireRegion
	// Environment is the deployment environment.
	Environment string
	// Version is the service version.
	Version string
	// Endpoint overrides the regional endpoint, for self-hosted installs.
	Endpoint string
}

// Logfire configures export to Pydantic Logfire.
func Logfire(cfg LogfireConfig) autotel.Option {
	if cfg.WriteToken == "" {
		return invalid("backends: Logfire write token is required; create one under Project Settings → Write Tokens")
	}
	if cfg.Service == "" {
		return invalid("backends: Logfire requires a service name")
	}
	if cfg.Endpoint == "" && cfg.Region == "" {
		return invalid("backends: Logfire region is required (%q or %q); a token is region-specific",
			LogfireUS, LogfireEU)
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		regional, ok := logfireEndpoints[cfg.Region]
		if !ok {
			return invalid("backends: unknown Logfire region %q; supported regions are %q and %q",
				cfg.Region, LogfireUS, LogfireEU)
		}
		endpoint = regional
	}

	return compose(
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(autotel.ProtocolHTTP),
		autotel.WithEndpoint(endpoint),
		autotel.WithInsecure(false),
		autotel.WithHeaders(map[string]string{"Authorization": cfg.WriteToken}),
	)
}

// --- Langfuse ----------------------------------------------------------------

// LangfuseRegion identifies a Langfuse Cloud region.
type LangfuseRegion string

// Langfuse Cloud regions.
const (
	LangfuseEU LangfuseRegion = "eu"
	LangfuseUS LangfuseRegion = "us"
)

var langfuseBaseURLs = map[LangfuseRegion]string{
	LangfuseEU: "https://cloud.langfuse.com",
	LangfuseUS: "https://us.cloud.langfuse.com",
}

const langfuseOTLPPath = "/api/public/otel"

// LangfuseConfig configures the Langfuse preset.
type LangfuseConfig struct {
	// PublicKey is the Langfuse public key (required), from Settings → API Keys.
	PublicKey string
	// SecretKey is the Langfuse secret key (required), from Settings → API Keys.
	SecretKey string
	// Service names the service (required).
	Service string
	// Region selects the Langfuse Cloud region. Defaults to LangfuseEU.
	Region LangfuseRegion
	// Environment is the deployment environment.
	Environment string
	// Version is the service version.
	Version string
	// BaseURL overrides the regional base URL, for self-hosted installs.
	BaseURL string
}

// Langfuse configures export to Langfuse for LLM tracing.
func Langfuse(cfg LangfuseConfig) autotel.Option {
	if cfg.PublicKey == "" {
		return invalid("backends: Langfuse public key is required; find it under Settings → API Keys")
	}
	if cfg.SecretKey == "" {
		return invalid("backends: Langfuse secret key is required; find it under Settings → API Keys")
	}
	if cfg.Service == "" {
		return invalid("backends: Langfuse requires a service name")
	}

	base := cfg.BaseURL
	if base == "" {
		region := cfg.Region
		if region == "" {
			region = LangfuseEU
		}
		regional, ok := langfuseBaseURLs[region]
		if !ok {
			return invalid("backends: unknown Langfuse region %q; supported regions are %q and %q",
				region, LangfuseEU, LangfuseUS)
		}
		base = regional
	}

	credentials := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))

	return compose(
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(autotel.ProtocolHTTP),
		autotel.WithEndpoint(trimTrailingSlash(base)+langfuseOTLPPath),
		autotel.WithInsecure(false),
		autotel.WithHeaders(map[string]string{"Authorization": "Basic " + credentials}),
	)
}

// --- PostHog -----------------------------------------------------------------

// PostHogRegion identifies a PostHog Cloud region.
type PostHogRegion string

// PostHog Cloud regions.
const (
	PostHogUS PostHogRegion = "us"
	PostHogEU PostHogRegion = "eu"
)

var postHogHosts = map[PostHogRegion]string{
	PostHogUS: "https://us.i.posthog.com",
	PostHogEU: "https://eu.i.posthog.com",
}

const postHogOTLPPath = "/i"

// PostHogConfig configures the PostHog preset.
type PostHogConfig struct {
	// ProjectToken is the PostHog project API key (required), from Project Settings.
	ProjectToken string
	// Service names the service (required).
	Service string
	// Region selects the PostHog Cloud region. Defaults to PostHogUS.
	Region PostHogRegion
	// Environment is the deployment environment.
	Environment string
	// Version is the service version.
	Version string
	// Host overrides the regional host, for self-hosted installs.
	Host string
}

// PostHog configures OTLP export to PostHog.
//
// This sends traces. To send product analytics events, use the PostHog
// subscriber in the subscribers package instead; the two are independent.
func PostHog(cfg PostHogConfig) autotel.Option {
	if cfg.ProjectToken == "" {
		return invalid("backends: PostHog project token is required; find it under Project Settings")
	}
	if cfg.Service == "" {
		return invalid("backends: PostHog requires a service name")
	}

	host := cfg.Host
	if host == "" {
		region := cfg.Region
		if region == "" {
			region = PostHogUS
		}
		regional, ok := postHogHosts[region]
		if !ok {
			return invalid("backends: unknown PostHog region %q; supported regions are %q and %q",
				region, PostHogUS, PostHogEU)
		}
		host = regional
	}

	endpoint := trimTrailingSlash(host)
	if !strings.HasSuffix(endpoint, postHogOTLPPath) {
		endpoint += postHogOTLPPath
	}

	return compose(
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(autotel.ProtocolHTTP),
		autotel.WithEndpoint(endpoint),
		autotel.WithInsecure(false),
		autotel.WithHeaders(map[string]string{"Authorization": "Bearer " + cfg.ProjectToken}),
	)
}

// --- OpenTelemetry Collector -------------------------------------------------

// CollectorConfig configures export to a self-run OpenTelemetry Collector.
type CollectorConfig struct {
	// Service names the service (required).
	Service string
	// Endpoint is the collector address. Defaults to http://localhost:4318.
	Endpoint string
	// Protocol selects OTLP over HTTP or gRPC. Defaults to HTTP.
	Protocol autotel.Protocol
	// Headers carries any authentication the collector requires.
	Headers map[string]string
	// Environment is the deployment environment.
	Environment string
	// Version is the service version.
	Version string
}

// Collector configures export to an OpenTelemetry Collector, which is the
// vendor-neutral option: point the collector at whichever backends you use and
// change destinations without redeploying the service.
func Collector(cfg CollectorConfig) autotel.Option {
	if cfg.Service == "" {
		return invalid("backends: Collector requires a service name")
	}

	endpoint := cfg.Endpoint
	protocol := cfg.Protocol
	if protocol == "" {
		protocol = autotel.ProtocolHTTP
	}
	if endpoint == "" {
		if protocol == autotel.ProtocolGRPC {
			endpoint = "http://localhost:4317"
		} else {
			endpoint = "http://localhost:4318"
		}
	}

	opts := []autotel.Option{
		identity(cfg.Service, cfg.Environment, cfg.Version),
		autotel.WithProtocol(protocol),
		autotel.WithEndpoint(endpoint),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, autotel.WithHeaders(cfg.Headers))
	}
	if strings.HasPrefix(endpoint, "https://") {
		opts = append(opts, autotel.WithInsecure(false))
	}

	return compose(opts...)
}
