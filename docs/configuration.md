# Configuration

Set options in code, in `autotel.yaml`, or with environment variables. Code beats YAML, and YAML beats the environment, so a variable fills in only what your code left unset. The one exception is `OTEL_{TRACES,METRICS,LOGS}_EXPORTER=none`, which always applies so an operator can turn a signal off.

## Environment variables

You can configure autotel with these variables alone. A *signal* is one of traces, metrics or logs.

| Variable | Effect |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Turns export on. A URL keeps its scheme, which decides TLS, and its path, which each signal's path goes on the end of. |
| `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT` | Sends one signal elsewhere, used exactly as written. Works without the base endpoint. An endpoint set in code or YAML overrides these. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` (default), `http/json` or `grpc`. Anything else fails `Init`, unless `WithProtocol` overrides it. `http/json` changes traces only: the Go exporters offer protobuf alone for metrics and logs. YAML takes `http/protobuf` or `grpc`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | Credentials, as `key=value,key=value`, percent-decoded. `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_HEADERS` add to them for one signal. |
| `OTEL_SERVICE_NAME` | Sets `service.name`. |
| `OTEL_RESOURCE_ATTRIBUTES` | Attributes that describe the service itself, such as `service.version=1.2,team=payments`, percent-decoded. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `always_on`, `always_off`, `traceidratio` and their `parentbased_` forms. Anything else fails `Init`, unless an option chose a sampler. See [sampling](production.md#choose-how-much-to-keep). |
| `OTEL_{TRACES,METRICS,LOGS}_EXPORTER` | `none` turns one signal off, for backends like Jaeger that take only traces. For traces this stops OTLP only, and exporters passed in code keep running. |
| `AUTOTEL_CONFIG_FILE` | Path to a YAML file. Without it, autotel reads `autotel.yaml` in the working directory. |
| `AUTOTEL_DEBUG` | `1` or `true` acts like `WithDebug(true)`, in production too. |
| `AUTOTEL_LOG_FILE_DIR` | Turns on [log files](production.md#write-logs-to-files) in this directory. |
| `AUTOTEL_LOG_FILE_ROTATION` | `minutely`, `hourly`, `daily` (default) or `never`. |
| `AUTOTEL_LOG_FILE_MAX_FILES` | Files to keep: 2 or more, or 0 for all. Default 7. |

## Options in code

Pass these to `autotel.Init`:

| Option | Effect |
| --- | --- |
| `WithService(name)` | Names your service. Defaults to the name in your binary's build info. |
| `WithServiceVersion(v)`, `WithEnvironment(env)` | Set `service.version` and `deployment.environment`. |
| `WithEndpoint(url)` | Sets the OTLP endpoint. A URL's scheme decides TLS. |
| `WithProtocol(autotel.ProtocolGRPC)` | Uses OTLP over gRPC. The default is HTTP. |
| `WithHeaders(map)` | Adds headers to each export, usually an API key. |
| `WithInsecure(false)` | Uses TLS for a bare `host:port` endpoint. |
| `WithMetrics(false)`, `WithLogs(false)` | Turn one signal off. |
| `WithDevtools()` | Sends everything to `npx autotel-devtools` on `127.0.0.1:4318`. |
| `WithDebug(true)` | Prints spans to stderr. On by default unless `ENV` or `ENVIRONMENT` is `production`. |

[Running in production](production.md) covers sampling, redaction and rate limits. [pkg.go.dev](https://pkg.go.dev/github.com/jagreehal/autotel-go/v2) lists every option.

## Use a vendor preset

The `backends` package sets the endpoint, headers and protocol for one vendor:

```go
import "github.com/jagreehal/autotel-go/v2/backends"

cleanup, err := autotel.Init(ctx, backends.Honeycomb(backends.HoneycombConfig{
	APIKey:  os.Getenv("HONEYCOMB_API_KEY"),
	Service: "orders",
}))
```

Presets exist for Honeycomb, Datadog, Grafana, Logfire, Langfuse, PostHog and any OpenTelemetry Collector. Each one takes the service name in its config, and a missing required field fails `Init`.

## Use a YAML file

Copy [autotel.yaml.example](../autotel.yaml.example) to `autotel.yaml` in your working directory, or point `AUTOTEL_CONFIG_FILE` at it. `${env:NAME}` and `${env:NAME:-default}` read environment variables. A file that doesn't parse fails `Init`.
