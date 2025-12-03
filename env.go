package autotel

import (
	"net/url"
	"os"
	"strings"
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

	// OTEL_EXPORTER_OTLP_PROTOCOL - optional enum ('http' | 'grpc')
	if proto := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))); proto != "" {
		switch proto {
		case string(ProtocolHTTP):
			cfg.Protocol = ProtocolHTTP
		case string(ProtocolGRPC):
			cfg.Protocol = ProtocolGRPC
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

	return cfg
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
		attrs[key] = value
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

	// Attempt full URL parsing first; fall back to manual cleanup.
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			if host := u.Host; host != "" {
				return strings.TrimSuffix(host, "/")
			}
		}
	}

	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "grpc://")
	raw = strings.TrimSuffix(raw, "/")
	return raw
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
		headers[key] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
