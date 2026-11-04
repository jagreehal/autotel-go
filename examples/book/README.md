# Observability Engineering, as running code

Chapter 15 gives you a nine-rung sampling ladder, and then you open your own service and find no function called climb-the-ladder.

This directory closes that gap for Go. Each example takes a concept from _Observability Engineering_, second edition, by Charity Majors, Liz Fong-Jones, George Miranda and Austin Parker, and runs it against [autotel-go](../../README.md). Each file sets up a small scene, instruments it, asserts what the chapter said would be true, and prints what it found.

A passing example prints. A broken concept exits non-zero. CI runs these, so a regression in the library shows up as a chapter that stopped working.

## Run them

You need Go 1.26 or later. You don't need a backend, an API key or Docker: each example exports into memory, and the SLO example injects its clock, so it computes a six-day budget burn without waiting for one.

```sh
go run ./examples/book/ch07-setup
go run ./examples/book/ch12-burn-alerts
go run ./examples/book/ch15-sampling
```

## Compare with the book's code

The authors' [companion code](https://resources.oreilly.com/examples/0636920722618) is mostly Go, so you can read their file and ours side by side.

| Ch  | Concept                            | Their Go code                              | Ours                                   |
| --- | ---------------------------------- | ------------------------------------------ | -------------------------------------- |
| 7   | Instrumenting with OpenTelemetry   | `1e/.../src/tracing.go`, 63 lines          | [`ch07-setup`](ch07-setup)             |
| 12  | Acting on SLO-based alerts         | `2e/chapter-12-slo-based-alerts`, 86 lines | [`ch12-burn-alerts`](ch12-burn-alerts) |
| 15  | Cheap and accurate enough sampling | `2e/chapter-15-sampling`, nine programs    | [`ch15-sampling`](ch15-sampling)       |

The chapter 15 programs carry scaffolding the authors disown: `09-head-and-tail/main.go` is 127 lines, and 55 of those sit under a comment reading *"scaffolding: stand-ins for your real code (not from the book)"*. We compare against the parts from the book.

## What each one shows

[`ch07-setup`](ch07-setup): the book's `tracing.go` builds a stdout exporter, a gRPC driver with TLS credentials, Honeycomb's endpoint and headers, a resource, a tracer provider and a composite propagator, with five error checks. You pay all that before you can record anything. The example asserts that one `backends.Honeycomb` call produces the same wire configuration, field by field, so CI fails if that stops being true.

[`ch12-burn-alerts`](ch12-burn-alerts): budget burn, two windows that must agree before anyone gets paged, and the predictive projection from the chapter's `isBurnViolation`. Their version takes a bucketed timeseries you assemble yourself, and this one records outcomes as they happen. It shows five failures per thousand consuming a 99.9% monthly budget in six days, a spike the long window declines to page on, and a deploy whose budget runs out in 9h43m. That's inside the 24-hour window, so it pages.

[`ch15-sampling`](ch15-sampling): all nine rungs, in the chapter's order, asserted against real exported spans. Rungs 5, 7 and 8 need a rate that adapts to observed volume, which `sampling.NewTargetRateSampler` provides.

## Why the assertions matter

Three features in this library have shipped doing nothing. The config merge discarded `WithSpanFilter`. `AdaptiveSampler` stored an error rate it never read, so a service asking to keep every failure dropped 90% of them. The rate limiter covered only spans created through `autotel.Start`. The tests checked that a value was set, and none checked that anything happened.

Writing chapter 15 as running code surfaced the sampler bug. Prose about a library can't fail, so it goes stale without telling you. These examples exit non-zero instead.
