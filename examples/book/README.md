# Observability Engineering, as running code

Chapter 15 gives you a nine-rung sampling ladder. You nod, and then you open
your own service and find no function called climb-the-ladder.

This directory closes that gap for Go. Each example takes a concept from
_Observability Engineering_, second edition, by Charity Majors, Liz Fong-Jones,
George Miranda, and Austin Parker, and runs it against
[autotel-go](../../README.md). Every file sets up a small scene, instruments it,
asserts the thing the chapter said would be true, and prints what it found.

A passing example prints. A broken concept exits non-zero, and these run in CI,
so a regression in the library shows up as a chapter that stopped working.

## What makes the Go version different

The authors' [companion code](https://resources.oreilly.com/examples/0636920722618)
is mostly Go. That means these examples do not have to translate anything: their
file and ours can sit side by side, in the same language, and you can read the
difference rather than take our word for it.

| Ch  | Concept                            | Their Go code                              | Ours                    |
| --- | ---------------------------------- | ------------------------------------------ | ----------------------- |
| 7   | Instrumenting with OpenTelemetry   | `1e/.../src/tracing.go`, 63 lines           | [`ch07-setup`](ch07-setup)             |
| 12  | Acting on SLO-based alerts         | `2e/chapter-12-slo-based-alerts`, 86 lines  | [`ch12-burn-alerts`](ch12-burn-alerts) |
| 15  | Cheap and accurate enough sampling | `2e/chapter-15-sampling`, nine programs     | [`ch15-sampling`](ch15-sampling)       |

Their line counts are quoted honestly. The chapter 15 programs are padded with
scaffolding the authors explicitly disown — `09-head-and-tail/main.go` is 127
lines, but 55 of those sit under a comment reading *"scaffolding: stand-ins for
your real code (not from the book)"*. We compare against the parts that are
actually from the book.

## Run them

```sh
go run ./examples/book/ch07-setup
go run ./examples/book/ch12-burn-alerts
go run ./examples/book/ch15-sampling
```

Go 1.25+. No backend, no API key, no Docker. Every example exports into memory,
and the SLO example injects its clock, so a six-day budget burn is computed
exactly rather than waited for.

## What each one shows

**[ch07-setup](ch07-setup)** — the book's `tracing.go` builds a stdout exporter,
a gRPC driver with TLS credentials, Honeycomb's endpoint and headers, a resource,
a tracer provider and a composite propagator, with five error checks. None of it
is the chapter's subject; it is the toll before you can record anything. The
example asserts that one `backends.Honeycomb` call produces the same wire
configuration, field by field, so the claim fails CI if it ever stops being true.

**[ch12-burn-alerts](ch12-burn-alerts)** — budget burn, two windows that have to
agree before anyone gets paged, and the predictive projection the chapter's own
`isBurnViolation` computes. Their version takes a bucketed timeseries you
assemble yourself; this records outcomes as they happen. The example shows five
failures per thousand consuming a 99.9% monthly budget in six days, a spike the
long window correctly refuses to page on, and a deploy whose budget runs out in
9h43m — inside the 24-hour window, so it pages.

**[ch15-sampling](ch15-sampling)** — all nine rungs, in the chapter's order,
asserted against real exported spans.

Three of them did not work when this file was first written. Rungs 5, 7 and 8
need a rate that adapts to observed volume, and the library only had static
rates, so the example said so plainly rather than skipping them. That gap is now
closed by `sampling.NewTargetRateSampler`, and the example climbs the ladder to
the top.

## Why the assertions matter

Two features in this library have shipped doing nothing at all — `WithSpanFilter`
was discarded by the config merge, and `AdaptiveSampler` stored an error rate it
never read, so a service asking to keep every failure dropped 90% of them. Both
were invisible to the tests, which checked that a value was set rather than that
anything happened.

Writing chapter 15 as running code is what surfaced the second one. That is the
argument for this directory: prose about a library cannot fail, so it goes stale
without telling you. These exit non-zero instead.
