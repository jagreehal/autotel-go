# Does autotel-go give better DX than vanilla OpenTelemetry?

An audit of the whole package, not just the three book chapters. Written before
publishing, so nobody has to walk a claim back afterwards.

Short answer: two claims are strong enough to publish today, one part of the
package is weaker than the README implies, and the blanket claim is not supported
yet. The gap is coverage, not writing.

## Where the package clearly wins

**Startup.** The authors' `tracing.go` is vanilla OpenTelemetry Go: a stdout
exporter, a gRPC driver carrying TLS credentials, two headers, a resource, a
provider, a composite propagator, five error checks. Sixty-three lines against
nine, and `ch07-setup` checks on every CI run that both produce the same endpoint,
headers, protocol and TLS. Plenty of libraries advertise less boilerplate. Few
wire the advert to a test that can fail.

**Vendor configuration.** Seven presets, each validating at `Init`. Ask for
Honeycomb with no API key and the process refuses to start instead of exporting
into the void for a week. With the vanilla SDK you assemble the endpoint, the
headers and the TLS setting per vendor, and a typo surfaces as silence.

**Sampling policy without a Collector.** The Go SDK ships `AlwaysSample`,
`NeverSample`, `TraceIDRatioBased` and `ParentBased`. All four decide when a span
starts, so none can keep your errors: a span that just started has no status and
no duration. Getting that policy with the vanilla SDK means deploying the
Collector and configuring its tail sampling processor, which is another binary to
run, version and monitor. This package applies the policy in process from two
options, and keeps the failed span's ancestors so the trace still reads.

**PII redaction in process** lands the same way. The vanilla answer is a Collector
processor.

## Where it is not a DX comparison at all

SLO tracking, burn-rate forecasts, workflow and saga spans, DLQ classification,
message ordering, consumer-group tracking, baggage schemas with PII detection,
product-event subscribers. OpenTelemetry does none of this and does not try to.

Bundling it is a product decision worth defending on its own terms. It is not
evidence that OpenTelemetry is harder to use.

## Where the README currently overstates

**Rate limiting and circuit breaking only cover some of your spans.** Both are
checked inside `autotel.Start`. Spans created by `middleware/httpclient.go`,
`messaging`, `workflow`, and every third-party instrumentation package go to
`otel.Tracer(...)` directly and skip both. Set a 100/sec limit and your actual
span volume is not capped. The README lists both under "Production features" with
no such caveat.

The fix is to move both into a span processor, where every span passes regardless
of who created it. That changes when the check runs, from span start to span end,
so it needs a decision rather than a patch.

**Middleware overlaps `otelhttp` and `otelgin`,** which are mature, widely
deployed and maintained by the OpenTelemetry contrib project. A thinner in-house
version is a risk to argue away, not a win to claim.

## The number that decides the blanket claim

Thirty-one public `Init` options. Twelve have an end-to-end test that drives
`Init` and asserts on exported spans. Nineteen do not.

Three features shipped doing nothing, and all three hid in the same blind spot: a
test that asserted a value was set rather than that anything happened.
`WithSpanFilter` was discarded by the config merge. `WithErrorRate`,
`WithSlowThreshold` and `WithSlowRate` were stored and never read. The in-memory
test exporter erased its spans on shutdown, so the obvious test sequence saw
nothing.

Whether a library does what you configured is part of its developer experience,
arguably the largest part. Nineteen untested options is the honest reason to hold
the general claim back.

## What to publish now

The startup comparison, and the sampling policy that otherwise costs you a
Collector. Both are true, both are checked by a machine on every push, and the
second is the stronger of the two while appearing in none of the drafts.

## What to build before claiming more

1. End-to-end tests for the remaining nineteen options, in the style of
   `pipeline_e2e_test.go`. That file exists because two dead features got through;
   finishing it is worth more than a fourth post.
2. A decision on rate limiting and circuit breaking: cover every span through a
   processor, or document that they cover `autotel.Start` only.
3. One example instrumenting the same request path twice, once against the SDK
   directly and once through this package, both asserted against the spans they
   export. That measures the loop developers live in rather than the one they
   touch at startup.

## The modest celebration

Sixty-three lines against nine, verified on every push against code the book's
authors wrote themselves. That one is earned. The rest is a good library with a
third of its surface unverified, which is a fine thing to be, and not yet a thing
to write a manifesto about.
