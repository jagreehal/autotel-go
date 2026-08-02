# Sixty-three lines before you record anything

Chapter 7 of _Observability Engineering_ is about instrumenting with
OpenTelemetry. Before it can be about that, it has to get OpenTelemetry running.
The authors' companion repository does this in a file called `tracing.go`, and
that file is sixty-three lines long.

Here is roughly what those lines do. A stdout exporter, so you can see something
locally. A gRPC driver, carrying TLS credentials built from the system cert pool.
Honeycomb's endpoint, hardcoded as `api.honeycomb.io:443`. Two headers, one for
the API key and one for the dataset, read out of the environment. A resource
carrying the service name. A tracer provider wiring the exporters together. A
composite propagator so trace context and baggage both cross process boundaries.
Five `log.Fatal` calls scattered through it, because every one of those steps
returns an error.

None of it is wrong. It is the honest minimum for talking to a vendor over OTLP,
and the book is right to show it rather than hide it behind a helper. But none of
it is the chapter's subject either. The chapter is about what to record and why.
Those sixty-three lines are the toll you pay before you can record anything at
all, and you pay it again in every service you own.

## The same configuration, in one call

```go
cleanup, err := autotel.Init(ctx, backends.Honeycomb(backends.HoneycombConfig{
    APIKey:  os.Getenv("HONEYCOMB_API_KEY"),
    Dataset: os.Getenv("HONEYCOMB_DATASET"),
    Service: "fibonacci",
}))
if err != nil {
    log.Fatal(err)
}
defer cleanup()
```

That is the whole of it, error handling included. Same endpoint, same two
headers, same protocol, same TLS.

The interesting question is not whether that is shorter. It obviously is. The
interesting question is whether it is still true a year from now, when
Honeycomb's ingest changes or someone refactors the preset and nobody thinks to
check the blog post that claimed it worked.

## So we made the claim executable

The comparison lives in the repository as a program that runs in CI:

```go
expect("endpoint", cfg.Endpoint, "api.honeycomb.io:443")
expect("x-honeycomb-team", cfg.Headers["x-honeycomb-team"], apiKey)
expect("x-honeycomb-dataset", cfg.Headers["x-honeycomb-dataset"], dataset)
expect("protocol", string(cfg.Protocol), string(autotel.ProtocolGRPC))
if cfg.Insecure {
    fail("TLS is off; the book passes credentials.NewClientTLSFromCert")
}
```

Each of those assertions corresponds to a line the book writes by hand. If the
preset ever stops producing the configuration the book's driver produces, the
build goes red and the claim in this post stops being publishable, which is the
correct outcome.

It needs no API key and touches no network. It compares configuration, not
exported spans, so it runs anywhere in under a second.

## What this is not

It is not an argument that you should never write the sixty-three lines. If you
are exporting to two backends with different sampling per signal, write them; a
preset that tried to cover that case would be worse than the explicit code.

It is an argument about defaults. The common case — one service, one vendor, the
configuration that vendor documents — should not cost you a file you have to
maintain and get right in every repository. The book pays that cost because it is
teaching OpenTelemetry. You are not teaching OpenTelemetry. You are trying to
find out why checkout is slow.

---

The example: [`examples/book/ch07-setup`](../../examples/book/ch07-setup).
The book's version: `1e/chapter-07-instrumentation-with-opentelemetry/src/tracing.go`
in the [companion repository](https://github.com/oreillymedia/observability_engineering).
