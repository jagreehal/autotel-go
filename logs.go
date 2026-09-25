package autotel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Logger returns a slog.Logger that exports records through the OTLP log
// pipeline set up by Init. Use the *Context methods (InfoContext, ErrorContext)
// and each record carries the trace and span ID of the span in ctx, so a
// backend can jump from a log line to its trace.
//
// A logger keeps the LoggerProvider that was global when it was built. Before
// the first Init that is OpenTelemetry's forwarding provider, which delegates
// to the pipeline Init then installs, so a package-level logger works. A
// logger built after one Init and before a second keeps the first pipeline.
// With logs disabled or no endpoint, records go nowhere.
func Logger(name string) *slog.Logger {
	return otelslog.NewLogger(name, otelslog.WithLoggerProvider(global.GetLoggerProvider()))
}

// setupLogs builds the OTLP log pipeline. It mirrors setupMetrics: nothing is
// exported unless an endpoint or a custom exporter is configured.
func setupLogs(ctx context.Context, res *resource.Resource, cfg *Config) (*sdklog.LoggerProvider, error) {
	if !cfg.LogsEnabled || strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_LOGS_EXPORTER")), "none") {
		return nil, nil
	}

	exportersList := cfg.LogExporters
	if len(exportersList) == 0 {
		if cfg.Endpoint == "" {
			return nil, nil
		}
		exp, err := newOTLPLogExporter(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create log exporter: %w", err)
		}
		exportersList = append(exportersList, exp)
	}

	providerOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	for _, exp := range exportersList {
		providerOpts = append(providerOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	}

	lp := sdklog.NewLoggerProvider(providerOpts...)
	global.SetLoggerProvider(lp)
	return lp, nil
}

func newOTLPLogExporter(ctx context.Context, cfg *Config) (sdklog.Exporter, error) {
	if cfg.Protocol == ProtocolHTTP {
		httpOpts := []otlploghttp.Option{
			otlploghttp.WithHeaders(cfg.Headers),
			otlploghttp.WithTimeout(cfg.BatchTimeout + 5*time.Second),
		}
		if endpointIsURL(cfg.Endpoint) {
			httpOpts = append(httpOpts, otlploghttp.WithEndpointURL(signalEndpointURL(cfg.Endpoint, logsPath)))
		} else {
			httpOpts = append(httpOpts, otlploghttp.WithEndpoint(cfg.Endpoint))
			if cfg.Insecure {
				httpOpts = append(httpOpts, otlploghttp.WithInsecure())
			}
		}
		return otlploghttp.New(ctx, httpOpts...)
	}

	grpcOpts := []otlploggrpc.Option{
		otlploggrpc.WithHeaders(cfg.Headers),
		otlploggrpc.WithTimeout(cfg.BatchTimeout + 5*time.Second),
	}
	if endpointIsURL(cfg.Endpoint) {
		grpcOpts = append(grpcOpts, otlploggrpc.WithEndpointURL(signalEndpointURL(cfg.Endpoint, logsPath)))
	} else {
		grpcOpts = append(grpcOpts, otlploggrpc.WithEndpoint(cfg.Endpoint))
		if cfg.Insecure {
			grpcOpts = append(grpcOpts, otlploggrpc.WithInsecure())
		}
	}
	return otlploggrpc.New(ctx, grpcOpts...)
}
