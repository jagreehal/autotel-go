# HTTP and gRPC

Add a middleware to your server and use a traced client for outgoing calls, and one trace follows a request across your services.

## Trace a net/http server

```go
handler := middleware.HTTPMiddleware("orders",
	middleware.WithSkipPaths("/healthz"),
	middleware.WithTraceResponseHeader(),
)(mux)
http.ListenAndServe(":8080", handler)
```

Each span is named after its route, such as `GET /users/{id}`, with `http.route` set. A million users share one span name, so your backend can group them. The route needs the middleware to wrap the `ServeMux` directly: a middleware between them that calls `r.WithContext` hides it, and the name stays `GET`.

| Option | Effect |
| --- | --- |
| `WithSkipPaths(paths...)` | Records no spans or metrics for these exact paths. Use it for health checks and other probes. |
| `WithTraceResponseHeader()` | Answers with a `traceparent` header, so a caller can quote the trace of a failed request. It exposes the trace and span IDs and no baggage. |
| `WithTrustForwardedFor()` | Takes `client.address` from `X-Forwarded-For`. Add it only behind a proxy that overwrites that header, because any client can send it. Without it, `client.address` is the TCP peer. |

Query strings are not recorded.

## Trace Chi, Gin or Echo

These wrap the maintained instrumentations: `otelchi`, contrib `otelgin` and LabStack `echo-otel/v4`. `Init` installs the propagators they need.

```go
r := chi.NewRouter()
r.Use(middleware.ChiMiddlewareOn(r, "orders")) // names spans from route templates

g := gin.Default()
g.Use(middleware.GinMiddleware("orders"))

e := echo.New()
e.Use(middleware.EchoMiddleware("orders"))
```

Each framework decides the client address itself, and `WithTrustForwardedFor` doesn't apply:

- Chi: `otelchi` records `X-Forwarded-For` as `http.client_ip`, so strip the header at your edge if clients can reach Chi directly.
- Gin: see `SetTrustedProxies`.
- Echo: see `IPExtractor`.

## Trace gRPC

```go
server := grpc.NewServer(grpc.StatsHandler(middleware.GRPCServerHandler()))

conn, err := grpc.NewClient("localhost:50051",
	grpc.WithStatsHandler(middleware.GRPCClientHandler()),
)
```

## Call other services

The next service joins your trace only if your request carries the W3C `traceparent` header. Pick the approach that fits your client. The first three also create a span for the call.

| You have | Use |
| --- | --- |
| No client yet | `middleware.NewHTTPClient()` |
| An `*http.Client` to keep | `middleware.WrapHTTPClient(client)` |
| Your own transport settings | `middleware.NewHTTPTransport(base)` |
| A client you can't change | `middleware.InjectHeaders(ctx, req)` before you send |

```go
client := middleware.NewHTTPClient()
resp, err := client.Post(ctx, url, "application/json", body) // also Get, Put, Patch, Delete and Do

wrapped := middleware.WrapHTTPClient(&http.Client{Timeout: 60 * time.Second}) // keeps its settings

custom := &http.Client{
	Transport: middleware.NewHTTPTransport(&http.Transport{MaxIdleConns: 100}),
	Timeout:   30 * time.Second,
}

req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
middleware.InjectHeaders(ctx, req)
resp, err = http.DefaultClient.Do(req)
```

Requests carry `traceparent`, `tracestate` and `baggage`. Spans record the URL without its user info or query string, and a status of 400 or above marks the span as failed.

Pass these options to `NewHTTPClient`. `WrapHTTPClient` and `NewHTTPTransport` take them too, but ignore `WithTimeout` and `WithBaseTransport`.

| Option | Effect |
| --- | --- |
| `WithTimeout(d)` | Sets the request timeout. Default 30 seconds. |
| `WithSpanNameFormatter(fn)` | Names spans your way. Default `HTTP GET`. |
| `WithoutSpans()` | Sends the headers and creates no span. |
| `WithResponseStatus()` | Records the HTTP status on the span. On by default. |
| `WithBaseTransport(rt)` | Sets the transport underneath. |
| `WithPropagator(p)` | Replaces the propagator that writes the headers. |
