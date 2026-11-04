package autotel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// receiver records what reached a fake OTLP endpoint.
type receiver struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	_, _ = io.Copy(io.Discard, req.Body)
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
}

func (r *receiver) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.URL.Path)
	}
	return out
}

func (r *receiver) header(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return ""
	}
	return r.requests[0].Header.Get(key)
}

func newReceiver(t *testing.T) (*receiver, string) {
	t.Helper()
	r := &receiver{}
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return r, server.URL
}

// emit sends one span, metric and log through a freshly built pipeline.
func emit(t *testing.T, opts ...Option) {
	t.Helper()
	opts = append([]Option{WithDebug(false), WithService("env-test")}, opts...)
	_, err := Init(context.Background(), opts...)
	require.NoError(t, err)
	ctx, span := Start(context.Background(), "work")
	Logger("env-test").InfoContext(ctx, "hello")
	Meter().Counter(ctx, "work.count", 1, nil)
	span.End()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = Shutdown(shutdownCtx)
}

// sniffer records the first bytes of every connection, for telling TLS from
// plaintext without completing either.
type sniffer struct {
	listener net.Listener
	mu       sync.Mutex
	first    []string
}

func newSniffer(t *testing.T) *sniffer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	s := &sniffer{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 3)
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := io.ReadFull(conn, buf); err == nil {
					s.mu.Lock()
					s.first = append(s.first, string(buf))
					s.mu.Unlock()
				}
			}()
		}
	}()
	return s
}

func (s *sniffer) address() string { return s.listener.Addr().String() }

// expectEvery waits for count connections, and fails if any began otherwise.
func (s *sniffer) expectEvery(t *testing.T, want string, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.first) >= count
	}, 5*time.Second, 10*time.Millisecond, "too few exporters connected")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.first {
		assert.True(t, strings.HasPrefix(got, want), "connection began %q, want %q", got, want)
	}
}

const tlsHandshake = "\x16"

// An https endpoint in the environment carries credentials: it must stay TLS.
func TestEnvHTTPSEndpointKeepsTLS(t *testing.T) {
	s := newSniffer(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://"+s.address())
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer s3cret")
	emit(t)
	s.expectEvery(t, tlsHandshake, 3)
}

// The exporters read OTEL_* themselves. An http:// variable must not turn a
// TLS endpoint from code into plaintext.
func TestTLSOptionSurvivesPlaintextEnv(t *testing.T) {
	for name, env := range map[string][2]string{
		"base endpoint":    {"OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318"},
		"traces endpoint":  {"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://otel-collector:4318/v1/traces"},
		"insecure setting": {"OTEL_EXPORTER_OTLP_INSECURE", "true"},
	} {
		for _, protocol := range []Protocol{ProtocolHTTP, ProtocolGRPC} {
			t.Run(fmt.Sprintf("%s %s", name, protocol), func(t *testing.T) {
				s := newSniffer(t)
				t.Setenv(env[0], env[1])
				emit(t, WithEndpoint(s.address()), WithInsecure(false), WithProtocol(protocol),
					WithHeaders(map[string]string{"x-api-key": "s3cret"}))
				s.expectEvery(t, tlsHandshake, 3)
			})
		}
	}
}

func TestPathFromPerSignalEnvDoesNotFollowAnOption(t *testing.T) {
	r, url := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://elsewhere:4318/custom/ingest")
	emit(t, WithEndpoint(strings.TrimPrefix(url, "http://")))
	assert.ElementsMatch(t, []string{"/v1/traces", "/v1/metrics", "/v1/logs"}, r.paths())
}

func TestSanitizeEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.example.com/otlp/": "https://api.example.com/otlp",
		"http://localhost:4318":         "http://localhost:4318",
		"grpc://collector:4317":         "collector:4317",
		"GRPC://collector:4317":         "collector:4317",
		"grpcs://collector:4317":        "https://collector:4317",
		"HTTPS://u:p@host/x":            "https://u:p@host/x",
		"localhost:4318/":               "localhost:4318",
	} {
		assert.Equal(t, want, sanitizeEndpoint(in), in)
	}
}

func TestEnvEndpointKeepsItsPathForEverySignal(t *testing.T) {
	r, url := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url+"/otlp")
	emit(t)
	assert.ElementsMatch(t, []string{"/otlp/v1/traces", "/otlp/v1/metrics", "/otlp/v1/logs"}, r.paths())
}

func TestEnvPerSignalEndpointIsUsedVerbatim(t *testing.T) {
	base, baseURL := newReceiver(t)
	traces, tracesURL := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", baseURL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", tracesURL+"/custom/ingest")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "x-traces=yes")
	emit(t)
	assert.Equal(t, []string{"/custom/ingest"}, traces.paths())
	assert.Equal(t, "yes", traces.header("x-traces"))
	assert.ElementsMatch(t, []string{"/v1/metrics", "/v1/logs"}, base.paths())
}

func TestEnvPerSignalEndpointWorksWithoutABase(t *testing.T) {
	r, url := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", url+"/v1/traces")
	emit(t)
	assert.Equal(t, []string{"/v1/traces"}, r.paths())
}

func TestExplicitEndpointBeatsPerSignalEnv(t *testing.T) {
	explicit, explicitURL := newReceiver(t)
	env, envURL := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", envURL+"/v1/traces")
	emit(t, WithEndpoint(explicitURL))
	assert.Empty(t, env.paths())
	assert.Contains(t, explicit.paths(), "/v1/traces")
}

func TestEnvExporterNoneSilencesOneSignal(t *testing.T) {
	for _, tc := range []struct {
		variable string
		want     []string
	}{
		{"OTEL_TRACES_EXPORTER", []string{"/v1/metrics", "/v1/logs"}},
		{"OTEL_METRICS_EXPORTER", []string{"/v1/traces", "/v1/logs"}},
		{"OTEL_LOGS_EXPORTER", []string{"/v1/traces", "/v1/metrics"}},
	} {
		t.Run(tc.variable, func(t *testing.T) {
			r, url := newReceiver(t)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
			t.Setenv(tc.variable, "none")
			emit(t)
			assert.ElementsMatch(t, tc.want, r.paths())
		})
	}
}

func TestEnvHeadersArePercentDecoded(t *testing.T) {
	r, url := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Basic%20dG9r+ZW4=,bad=100%")
	emit(t)
	assert.Equal(t, "Basic dG9r+ZW4=", r.header("Authorization"), "'+' is base64, not a space")
	assert.Equal(t, "100%", r.header("bad"), "an invalid escape stays as written")
}

func TestEnvProtocol(t *testing.T) {
	for value, want := range map[string]Protocol{
		"http": ProtocolHTTP, "http/protobuf": ProtocolHTTP, "http/json": ProtocolHTTP, "grpc": ProtocolGRPC,
	} {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", value)
		cfg := resolveEnvConfig()
		assert.Empty(t, cfg.OptionErrors(), value)
		assert.Equal(t, want, cfg.Protocol, value)
	}
}

func TestInvalidEnvFailsInit(t *testing.T) {
	for variable, value := range map[string]string{
		"OTEL_EXPORTER_OTLP_PROTOCOL": "bogus",
		"OTEL_TRACES_SAMPLER":         "sometimes",
		"OTEL_TRACES_SAMPLER_ARG":     "2",
		"AUTOTEL_LOG_FILE_ROTATION":   "weekly",
		"AUTOTEL_LOG_FILE_MAX_FILES":  "1",
	} {
		t.Run(variable, func(t *testing.T) {
			if variable == "OTEL_TRACES_SAMPLER_ARG" {
				t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
			}
			if strings.HasPrefix(variable, "AUTOTEL_LOG_FILE") {
				t.Setenv("AUTOTEL_LOG_FILE_DIR", t.TempDir())
			}
			t.Setenv(variable, value)
			_, err := Init(context.Background(), WithDebug(false))
			require.Error(t, err)
			assert.Contains(t, err.Error(), variable)
		})
	}
}

// keepingExporter holds on to spans past Shutdown, unlike tracetest's.
type keepingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *keepingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *keepingExporter) Shutdown(context.Context) error { return nil }

func exportedSpans(t *testing.T, total int, opts ...Option) int {
	t.Helper()
	exporter := &keepingExporter{}
	opts = append([]Option{WithService("sampling"), WithSpanExporters(exporter)}, opts...)
	_, err := Init(context.Background(), opts...)
	require.NoError(t, err)
	tracer := otel.Tracer("sampling")
	for range total {
		_, span := tracer.Start(context.Background(), "routine")
		span.End()
	}
	require.NoError(t, Shutdown(context.Background()))
	return len(exporter.spans)
}

func TestDefaultKeepsEverySpanInProduction(t *testing.T) {
	t.Setenv("ENV", "production")
	assert.Equal(t, 200, exportedSpans(t, 200))
}

func TestEnvSampler(t *testing.T) {
	for _, tc := range []struct {
		sampler, arg string
		min, max     int
	}{
		{"always_on", "", 500, 500},
		{"always_off", "", 0, 0},
		{"parentbased_always_off", "", 0, 0},
		{"parentbased_always_on", "", 500, 500},
		{"traceidratio", "0", 0, 0},
		{"traceidratio", "", 500, 500},
		{"parentbased_traceidratio", "0.5", 180, 320},
	} {
		t.Run(fmt.Sprintf("%s=%s", tc.sampler, tc.arg), func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER", tc.sampler)
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", tc.arg)
			// No WithDebug: debug that is on only because ENV isn't production
			// must not overrule a sampler someone chose.
			got := exportedSpans(t, 500)
			assert.GreaterOrEqual(t, got, tc.min)
			assert.LessOrEqual(t, got, tc.max)
		})
	}
}

func TestOptionSamplerBeatsEnvSampler(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	assert.Equal(t, 50, exportedSpans(t, 50, WithDebug(false), WithSampler(sdktrace.AlwaysSample())))
}

func TestRequestedDebugBeatsChosenSampler(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	assert.Equal(t, 50, exportedSpans(t, 50, WithDebug(true)))
}

func TestAutotelDebugWorksInProduction(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("AUTOTEL_DEBUG", "1")
	assert.True(t, ShouldEnableDebug(nil))
}

func resourceValue(t *testing.T, key attribute.Key, opts ...Option) string {
	t.Helper()
	exporter := &keepingExporter{}
	opts = append([]Option{WithDebug(false), WithSpanExporters(exporter)}, opts...)
	_, err := Init(context.Background(), opts...)
	require.NoError(t, err)
	_, span := Start(context.Background(), "work")
	span.End()
	require.NoError(t, Shutdown(context.Background()))
	require.NotEmpty(t, exporter.spans)
	value, _ := exporter.spans[0].Resource().Set().Value(key)
	return value.AsString()
}

func TestCodeBeatsEnvForServiceIdentity(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-attrs,service.version=9")
	assert.Equal(t, "from-code", resourceValue(t, "service.name", WithService("from-code")))
	assert.Equal(t, "1.2.3", resourceValue(t, "service.version", WithServiceVersion("1.2.3")))
}

func TestEnvIdentityPrecedence(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-attrs,service.version=9,team=pay%20ments")
	assert.Equal(t, "from-attrs", resourceValue(t, "service.name"))
	assert.Equal(t, "9", resourceValue(t, "service.version"))
	assert.Equal(t, "pay ments", resourceValue(t, "team"))
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	assert.Equal(t, "from-env", resourceValue(t, "service.name"))
}

func TestServiceNameFromBuildInfo(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/acme/orders":        "orders",
		"github.com/acme/orders/v3":     "orders",
		"github.com/acme/mono/cmd/api":  "api",
		"github.com/acme/orders/v3/cmd": "cmd",
		"command-line-arguments":        "",
		"":                              "",
		"github.com/acme/orders.test":   "",
	} {
		assert.Equal(t, want, serviceNameFromPackage(in), in)
	}
}

func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	var out strings.Builder
	var mu sync.Mutex
	previous := log.Writer()
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return out.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(previous) })
	return &out
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestInitSaysWhereTelemetryGoes(t *testing.T) {
	out := captureLog(t)
	_, err := Init(context.Background(), WithDebug(false), WithService("orders"))
	require.NoError(t, err)
	require.NoError(t, Shutdown(context.Background()))
	assert.Contains(t, out.String(), "orders: no OTLP endpoint, telemetry stays local")
	assert.Contains(t, out.String(), "OTEL_EXPORTER_OTLP_ENDPOINT")

	out.Reset()
	_, url := newReceiver(t)
	secret := strings.Replace(url, "http://", "http://user:hunter2@", 1)
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	_, err = Init(context.Background(), WithDebug(false), WithService("orders"), WithEndpoint(secret))
	require.NoError(t, err)
	_ = Shutdown(context.Background())
	assert.Contains(t, out.String(), "exporting traces to "+url+"/v1/traces")
	assert.Contains(t, out.String(), "logs to "+url+"/v1/logs")
	assert.NotContains(t, out.String(), "metrics to")
	assert.NotContains(t, out.String(), "hunter2")
}

// The default in the explicit layer must not read as a choice that beats env.
func TestEnvGRPCProtocolReachesTheExporter(t *testing.T) {
	s := newSniffer(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+s.address())
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	emit(t)
	s.expectEvery(t, "PRI", 3)
}

func TestProtocolPrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	cfg := defaultConfig()
	assert.Equal(t, ProtocolGRPC, resolveAndMergeConfig(cfg).Protocol)

	cfg = defaultConfig()
	WithProtocol(ProtocolHTTP)(cfg)
	assert.Equal(t, ProtocolHTTP, resolveAndMergeConfig(cfg).Protocol, "an option beats env, even when it names the default")
}

func TestYAMLIdentitySurvivesTheExplicitDefaults(t *testing.T) {
	merged := mergeConfigs(defaultConfig(), &Config{ServiceVersion: "3.1.0", Environment: "staging", Protocol: ProtocolGRPC}, nil)
	assert.Equal(t, "3.1.0", merged.ServiceVersion)
	assert.Equal(t, "staging", merged.Environment)
	assert.Equal(t, ProtocolGRPC, merged.Protocol)

	explicit := defaultConfig()
	explicit.ServiceVersion, explicit.Environment = "4.0.0", "prod"
	merged = mergeConfigs(explicit, &Config{ServiceVersion: "3.1.0", Environment: "staging"}, nil)
	assert.Equal(t, "4.0.0", merged.ServiceVersion)
	assert.Equal(t, "prod", merged.Environment)
}

func TestResourceNamesTheSDK(t *testing.T) {
	assert.Equal(t, "go", resourceValue(t, "telemetry.sdk.language", WithService("x")))
}

func TestGRPCAnnouncementShowsNoHTTPPath(t *testing.T) {
	assert.Equal(t, "http://collector:4317", displayEndpoint("http://collector:4317/v1/traces", ProtocolGRPC))
	assert.Equal(t, "https://x/otlp/v1/traces", displayEndpoint("https://u:p@x/otlp/v1/traces?k=v", ProtocolHTTP))
	assert.Equal(t, "http://collector:4317", displayEndpoint("http://collector:4317#x", ProtocolHTTP))
}

func TestStartupLineHidesCredentialsInABareEndpoint(t *testing.T) {
	out := captureLog(t)
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithEndpoint("user:hunter2@127.0.0.1:1?api_key=hunter3"))
	require.NoError(t, err)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = Shutdown(shutdownCtx)
	assert.NotContains(t, out.String(), "hunter2")
	assert.NotContains(t, out.String(), "hunter3")
}

func TestExplicitEndpointDropsPerSignalHeaders(t *testing.T) {
	r, url := newReceiver(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "x-internal=token")
	emit(t, WithEndpoint(url))
	require.Contains(t, r.paths(), "/v1/traces")
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, request := range r.requests {
		assert.Empty(t, request.Header.Get("x-internal"), request.URL.Path)
	}
}

func TestHandBuiltConfigStillExports(t *testing.T) {
	exporter := &keepingExporter{}
	_, err := InitWithConfig(context.Background(), &Config{ServiceName: "x", SpanExporters: []sdktrace.SpanExporter{exporter}})
	require.NoError(t, err)
	_, span := Start(context.Background(), "work")
	span.End()
	require.NoError(t, Shutdown(context.Background()))
	assert.Len(t, exporter.spans, 1)
}

func TestSamplerSetOnTheStructIsAChoice(t *testing.T) {
	exporter := &keepingExporter{}
	cfg := DefaultConfig()
	cfg.ServiceName = "x"
	cfg.Sampler = sdktrace.NeverSample()
	cfg.SpanExporters = []sdktrace.SpanExporter{exporter}
	_, err := InitWithConfig(context.Background(), cfg) // ENV unset, so debug is on by default
	require.NoError(t, err)
	_, span := Start(context.Background(), "work")
	span.End()
	require.NoError(t, Shutdown(context.Background()))
	assert.Empty(t, exporter.spans)
}

func TestOptionsNamingADefaultStillWin(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=production,service.version=9")
	assert.Equal(t, "development", resourceValue(t, "deployment.environment", WithEnvironment("development")))
	assert.Equal(t, "0.0.0", resourceValue(t, "service.version", WithServiceVersion("0.0.0")))

	explicit := defaultConfig()
	WithEnvironment("development")(explicit)
	WithServiceVersion("0.0.0")(explicit)
	merged := mergeConfigs(explicit, &Config{ServiceVersion: "3.1.0", Environment: "staging"}, nil)
	assert.Equal(t, "0.0.0", merged.ServiceVersion)
	assert.Equal(t, "development", merged.Environment)
}

func TestResourceNameInCodeBeatsEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	assert.Equal(t, "from-code", resourceValue(t, "service.name",
		func(c *Config) { c.ResourceAttributes = map[string]string{"service.name": "from-code"} }))
}

func TestNewEnvironmentKeySuppressesTheDefault(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=production")
	assert.Empty(t, resourceValue(t, "deployment.environment"))
	assert.Equal(t, "production", resourceValue(t, "deployment.environment.name"))
}

func TestHeadersMergeIgnoringCase(t *testing.T) {
	for range 30 {
		got := mergeHeaders(map[string]string{"authorization": "env"}, map[string]string{"Authorization": "code"})
		assert.Equal(t, map[string]string{"Authorization": "code"}, got)
	}
}

func TestVendorPresetRespectsChoices(t *testing.T) {
	t.Setenv("DD_API_KEY", "from-env")
	cfg := defaultConfig()
	WithBackend("datadog")(cfg)
	WithProtocol(ProtocolHTTP)(cfg)
	WithHeaders(map[string]string{"DD-API-KEY": "from-code"})(cfg)
	merged := resolveAndMergeConfig(cfg)
	assert.Equal(t, ProtocolHTTP, merged.Protocol)
	assert.Equal(t, "from-code", merged.Headers["DD-API-KEY"])
}

func TestBrokenYAMLFailsInit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autotel.yaml")
	require.NoError(t, os.WriteFile(path, []byte("exporter: [unclosed"), 0o600))
	t.Setenv("AUTOTEL_CONFIG_FILE", path)
	t.Chdir(dir)
	_, err := Init(context.Background(), WithDebug(false))
	assert.Error(t, err)

	require.NoError(t, os.WriteFile(path, []byte("exporter:\n  protocol: grcp\n"), 0o600))
	_, err = Init(context.Background(), WithDebug(false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grcp")
}

func TestBadEnvIsForgivenWhenAnOptionOutranksIt(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "xray")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "bogus")
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithSampler(sdktrace.AlwaysSample()), WithProtocol(ProtocolHTTP))
	require.NoError(t, err)
	require.NoError(t, Shutdown(context.Background()))
}

func TestCommandLineStaysOffTheResource(t *testing.T) {
	assert.Empty(t, resourceValue(t, "process.command_args", WithService("x")))
	assert.Empty(t, resourceValue(t, "process.owner", WithService("x")))
	assert.NotEmpty(t, resourceValue(t, "process.runtime.name", WithService("x")))
}

func TestPIIRedactionCoversSpansFromAnywhere(t *testing.T) {
	exporter := &keepingExporter{}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithSpanExporters(exporter), WithPIIRedaction())
	require.NoError(t, err)

	// A span from otel directly, as middleware creates them.
	_, span := otel.Tracer("mw").Start(context.Background(), "GET /users/jo@example.com")
	span.SetAttributes(
		attribute.String("url.path", "/users/jo@example.com"),
		attribute.StringSlice("emails", []string{"jo@example.com"}),
	)
	span.AddEvent("seen", trace.WithAttributes(attribute.String("who", "jo@example.com")))
	span.SetStatus(codes.Error, "no user jo@example.com")
	span.End()
	require.NoError(t, Shutdown(context.Background()))

	require.Len(t, exporter.spans, 1)
	got := exporter.spans[0]
	dump := got.Name() + got.Status().Description
	for _, kv := range got.Attributes() {
		dump += kv.Value.String()
	}
	for _, event := range got.Events() {
		for _, kv := range event.Attributes {
			dump += kv.Value.String()
		}
	}
	assert.NotContains(t, dump, "jo@example.com")
	assert.Contains(t, dump, "REDACTED")
}

func TestFailedInitLeavesARunningPipelineAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	exporter := &metricNames{names: map[string]bool{}}
	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithMetricExporters(exporter))
	require.NoError(t, err)

	_, err = Init(context.Background(), WithDebug(false), WithService("x"),
		WithMetricExporters(&metricNames{names: map[string]bool{}}),
		WithLogFile(LogFile{Dir: filepath.Join(file, "sub")}))
	require.Error(t, err)

	Meter().Counter(context.Background(), "still.counted", 1, nil)
	require.NoError(t, Shutdown(context.Background()))
	assert.True(t, exporter.names["still.counted"])
}

type stuckExporter struct{ keepingExporter }

func (s *stuckExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStuckTraceExportDoesNotStarveLogs(t *testing.T) {
	r, url := newReceiver(t)
	_, err := Init(context.Background(), WithDebug(false), WithService("x"),
		WithEndpoint(url), WithSpanExporters(&stuckExporter{}), WithMetrics(false))
	require.NoError(t, err)
	_, span := Start(context.Background(), "work")
	span.End()
	Logger("x").Info("must arrive")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = Shutdown(ctx)
	assert.Contains(t, r.paths(), "/v1/logs")
}

type countingTracker struct {
	EventTracker
	shutdowns atomic.Int64
}

func (c *countingTracker) Shutdown(context.Context) error {
	c.shutdowns.Add(1)
	return nil
}

func TestStaleCleanupLeavesTheNewTrackerAlone(t *testing.T) {
	first, second := &countingTracker{}, &countingTracker{}
	previous := queueFactory
	t.Cleanup(func() { queueFactory = previous })

	queueFactory = func(*Config, []Subscriber) EventTracker { return first }
	cleanup, err := Init(context.Background(), WithDebug(false), WithService("x"), WithSubscribers(struct{ Subscriber }{}))
	require.NoError(t, err)
	queueFactory = func(*Config, []Subscriber) EventTracker { return second }
	_, err = Init(context.Background(), WithDebug(false), WithService("x"), WithSubscribers(struct{ Subscriber }{}))
	require.NoError(t, err)

	cleanup()
	assert.EqualValues(t, 1, first.shutdowns.Load())
	assert.Zero(t, second.shutdowns.Load())
	globalTrackerMu.RLock()
	assert.Same(t, second, globalTracker, "Track must keep working")
	globalTrackerMu.RUnlock()
	require.NoError(t, Shutdown(context.Background()))
	assert.EqualValues(t, 1, second.shutdowns.Load())
}

func TestCustomSubscriberWithoutTheQueueFailsInit(t *testing.T) {
	previous := queueFactory
	t.Cleanup(func() { queueFactory = previous })
	queueFactory = nil

	_, err := Init(context.Background(), WithDebug(false), WithService("x"), WithSubscribers(struct{ Subscriber }{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `_ "github.com/jagreehal/autotel-go/v2/subscribers"`)
}
