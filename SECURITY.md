# Security policy

## Reporting a vulnerability

Report vulnerabilities privately, and don't open a public issue.

- Email jag.reehal+security@gmail.com, or
- use GitHub's private vulnerability reporting: [Security → Report a vulnerability](https://github.com/jagreehal/autotel-go/security/advisories/new).

Include the module version, a minimal reproducer and the impact you expect. You should get a reply within 7 days.

## Trust model

- autotel trusts W3C `baggage` from callers. With `WithDebugCapture`, an `autotel.debug` entry keeps that request's spans whatever the sampler decides, and `WithBaggageAttributes` copies entries onto spans. If callers outside your network can reach a service, strip `baggage` at your gateway or ingress.
- `HTTPMiddleware` records the TCP peer unless you add `WithTrustForwardedFor`, which believes whatever `X-Forwarded-For` says. Use it only behind a proxy that overwrites the header.
- `WithInsecure` applies only to a bare `host:port` endpoint and defaults to true for local collectors. Use an `https://` URL for anything that carries credentials: the scheme decides TLS.
- `OTEL_EXPORTER_OTLP_HEADERS` applies to whatever endpoint is in force, including one set in code. That is how a secret stays out of source, and it means the environment's credentials go to that endpoint. Per-signal headers don't: they go only with a per-signal endpoint from the environment.
- `database.SanitizeQuery` hides the rest of a query at anything dialects read differently. It's best effort, so keep secrets in bind parameters.
- `WithPIIRedaction` matches patterns in spans, logs, `autotel.Meter()` metrics and `autotel.Track` events. It misses formats it doesn't know, and instruments you build with `otel.Meter`.
- The resource leaves out the command line and process owner, which can hold secrets.

## Supported versions

Fixes go into the latest published version. Older versions do not get backports.
