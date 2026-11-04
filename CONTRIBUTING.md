# Contributing to autotel-go

Open an issue before you start on a major feature, so we can agree the approach first.

## Set up

You need Go 1.26 or later.

```bash
git clone https://github.com/<you>/autotel-go.git
cd autotel-go
go mod download
make test
```

`make lint` needs [golangci-lint](https://golangci-lint.run/) and skips with a message if you don't have it. CI runs v2.14.0.

## Find your way around

| Path | Holds |
| --- | --- |
| `autotel.go`, `span.go`, `tracer.go`, `options.go` | The core API |
| `middleware/` | HTTP, Chi, Gin, Echo and gRPC integrations |
| `sampling/`, `ratelimit/`, `circuitbreaker/`, `redaction/` | Production features |
| `backends/` | Vendor presets |
| `database/`, `genai/`, `messaging/`, `workflow/`, `webhook/`, `baggage/` | Integration packages |
| `slo/`, `analysis/`, `subscribers/`, `logging/` | Advanced features |
| `testing/` | Test helpers |
| `examples/`, `workshop/` | Runnable programs, and the workshop |

## Write your change

- Follow standard Go formatting and naming. `make format` runs `gofmt` and `goimports`.
- Add doc comments to exported names, and keep functions small.
- Add tests for new features, including error and edge cases. Use table-driven tests where they fit, and aim for over 80% coverage.
- Test what reaches an exporter. A new `With...` option fails `option_coverage_test.go` until you exercise it in `pipeline_e2e_test.go`, or list it in that test with the file that verifies it.
- Update the docs and `CHANGELOG.md` if users will notice the change.

## Open a pull request

1. Branch from `main`.
2. Run `make format`, `make lint` and `go test ./...`.
3. If you touched the library's behaviour, run `make workshop` and the [book examples](examples/book/README.md). CI runs both.
4. Describe what changed and why.
