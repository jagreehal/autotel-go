package autotel

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/sdk/trace"
)

// resolveEnvConfig reads OpenTelemetry environment variables and returns a Config.
// This is used for merging with YAML and explicit configs.
func resolveEnvConfig() *Config {
	cfg := &Config{}

	// OTEL_SERVICE_NAME - optional string
	if v := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); v != "" {
		cfg.ServiceName = v
	}

	// OTEL_EXPORTER_OTLP_ENDPOINT - optional URL
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); v != "" {
		cfg.Endpoint = sanitizeEndpoint(v)
	}

	// OTEL_EXPORTER_OTLP_PROTOCOL - http/protobuf, http/json, grpc, or the legacy http.
	// A typo fails Init: guessing would send telemetry somewhere it isn't heard.
	if proto := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))); proto != "" {
		switch proto {
		case string(ProtocolHTTP), "http/protobuf", "http/json":
			cfg.Protocol = ProtocolHTTP
		case string(ProtocolGRPC):
			cfg.Protocol = ProtocolGRPC
		default:
			cfg.protocolEnvErr = fmt.Errorf("autotel: OTEL_EXPORTER_OTLP_PROTOCOL=%q is not one of http/protobuf, http/json, grpc", proto)
		}
	}

	// OTEL_EXPORTER_OTLP_HEADERS - optional string
	if headers := parseHeadersEnv(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")); len(headers) > 0 {
		cfg.Headers = headers
	}

	// OTEL_RESOURCE_ATTRIBUTES - optional string (key1=value1,key2=value2)
	if attrs := parseResourceAttributesEnv(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")); len(attrs) > 0 {
		cfg.ResourceAttributes = attrs
	}

	cfg.signals.traces = signalFromEnv("TRACES")
	cfg.signals.metrics = signalFromEnv("METRICS")
	cfg.signals.logs = signalFromEnv("LOGS")

	// OTEL_TRACES_SAMPLER, OTEL_TRACES_SAMPLER_ARG
	if sampler, err := samplerFromEnv(); err != nil {
		cfg.samplerEnvErr = err
	} else if sampler != nil {
		cfg.Sampler = sampler
		cfg.samplerChosen = true
	}

	// AUTOTEL_LOG_FILE_DIR, _ROTATION, _MAX_FILES
	if file, err := logFileFromEnv(); err != nil {
		cfg.OptionError(err)
	} else {
		cfg.LogFile = file
	}

	return cfg
}

// signalFromEnv reads the variables that apply to one signal, named TRACES,
// METRICS or LOGS.
func signalFromEnv(name string) signalEnv {
	exporter := os.Getenv("OTEL_" + name + "_EXPORTER")
	return signalEnv{
		Endpoint: sanitizeEndpoint(os.Getenv("OTEL_EXPORTER_OTLP_" + name + "_ENDPOINT")),
		Headers:  parseHeadersEnv(os.Getenv("OTEL_EXPORTER_OTLP_" + name + "_HEADERS")),
		Disabled: strings.EqualFold(strings.TrimSpace(exporter), "none"),
	}
}

// samplerFromEnv builds the sampler named by OTEL_TRACES_SAMPLER, or nil when
// the variable is unset.
func samplerFromEnv() (trace.Sampler, error) {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	if name == "" {
		return nil, nil
	}

	ratio := func() (float64, error) {
		raw := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
		if raw == "" {
			return 1, nil
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || value > 1 {
			return 0, fmt.Errorf("autotel: OTEL_TRACES_SAMPLER_ARG=%q is not a ratio from 0 to 1", raw)
		}
		return value, nil
	}

	switch name {
	case "always_on":
		return trace.AlwaysSample(), nil
	case "always_off":
		return trace.NeverSample(), nil
	case "parentbased_always_on":
		return trace.ParentBased(trace.AlwaysSample()), nil
	case "parentbased_always_off":
		return trace.ParentBased(trace.NeverSample()), nil
	case "traceidratio", "parentbased_traceidratio":
		value, err := ratio()
		if err != nil {
			return nil, err
		}
		if name == "traceidratio" {
			return trace.TraceIDRatioBased(value), nil
		}
		return trace.ParentBased(trace.TraceIDRatioBased(value)), nil
	}
	return nil, fmt.Errorf("autotel: OTEL_TRACES_SAMPLER=%q is not one of always_on, always_off, "+
		"traceidratio, parentbased_always_on, parentbased_always_off, parentbased_traceidratio", name)
}

// logFileFromEnv reads AUTOTEL_LOG_FILE_*. Nothing is written unless
// AUTOTEL_LOG_FILE_DIR is set.
func logFileFromEnv() (*LogFile, error) {
	dir := strings.TrimSpace(os.Getenv("AUTOTEL_LOG_FILE_DIR"))
	if dir == "" {
		return nil, nil
	}

	file := &LogFile{Dir: dir}
	if raw := strings.TrimSpace(os.Getenv("AUTOTEL_LOG_FILE_ROTATION")); raw != "" {
		file.Rotation = LogRotation(strings.ToLower(raw))
		if !file.Rotation.valid() {
			return nil, fmt.Errorf("autotel: AUTOTEL_LOG_FILE_ROTATION=%q is not one of minutely, hourly, daily, never", raw)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AUTOTEL_LOG_FILE_MAX_FILES")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value == 1 {
			return nil, fmt.Errorf("autotel: AUTOTEL_LOG_FILE_MAX_FILES=%q must be 2 or more, or 0 to keep all", raw)
		}
		file.MaxFiles = value
		if value == 0 {
			file.MaxFiles = KeepAllLogFiles
		}
	}
	return file, nil
}

// majorVersionSuffix matches the /v2 a module path ends in from version 2 on.
var majorVersionSuffix = regexp.MustCompile(`/v[0-9]+$`)

// serviceNameFromPackage names a service after its main package, or returns ""
// when the path says nothing useful: `go run file.go`, or a test binary.
func serviceNameFromPackage(pkg string) string {
	if pkg == "" || pkg == "command-line-arguments" || strings.HasSuffix(pkg, ".test") {
		return ""
	}
	return path.Base(majorVersionSuffix.ReplaceAllString(pkg, ""))
}

// applyBuildInfo fills in a service name and version nobody configured from
// what the Go toolchain stamped into the binary.
func applyBuildInfo(cfg *Config) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if _, set := cfg.ResourceAttributes["service.name"]; !set && cfg.ServiceName == defaultServiceName {
		if name := serviceNameFromPackage(info.Path); name != "" {
			cfg.ServiceName = name
		}
	}
	if _, set := cfg.ResourceAttributes["service.version"]; !set && cfg.ServiceVersion == defaultServiceVersion {
		if version := info.Main.Version; version != "" && version != "(devel)" {
			cfg.ServiceVersion = version
		}
	}
}

// parseResourceAttributesEnv parses OTEL_RESOURCE_ATTRIBUTES env var
// Format: key1=value1,key2=value2
func parseResourceAttributesEnv(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	attrs := make(map[string]string)
	pairs := strings.Split(raw, ",")
	for _, pair := range pairs {
		trimmed := strings.TrimSpace(pair)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		attrs[key] = percentDecode(value)
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

func sanitizeEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// A URL stays a URL. Its scheme decides TLS and its path is the base every
	// signal path goes on, so reducing it to host:port would send credentials
	// meant for https:// over plain HTTP.
	for prefix, replacement := range map[string]string{
		"http://": "http://", "https://": "https://", "grpcs://": "https://", "grpc://": "",
	} {
		if len(raw) >= len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
			raw = replacement + raw[len(prefix):]
			break
		}
	}
	return strings.TrimRight(raw, "/")
}

// percentDecode undoes the percent-encoding the OTEL_* key=value lists use. It
// leaves '+' alone, since base64 credentials contain it, and keeps a value with
// an invalid escape as written.
func percentDecode(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func parseHeadersEnv(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	headers := make(map[string]string)
	pairs := strings.Split(raw, ",")
	for _, pair := range pairs {
		trimmed := strings.TrimSpace(pair)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		headers[key] = percentDecode(value)
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
