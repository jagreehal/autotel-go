# Alert on the budget, not the threshold

Every on-call rotation has the alert nobody trusts. Error rate above one percent
for five minutes. It fires during a deploy, it fires when a batch job runs, it
fires at 3am for a spike that resolved itself before anyone opened a laptop. So
people mute it, and then it fires for something real and nobody looks.

Chapters 11 and 12 of _Observability Engineering_ replace the threshold with a
budget. A 99.9% monthly objective does not mean "never fail". It means you may
fail one request in a thousand, and the interesting question is not whether you
are failing right now but whether you will run out of allowance before the month
ends.

## Five failures per thousand

Start with what the budget buys you. Serve a thousand requests, fail five of them
against a 99.9% target, and you are burning at five times the sustainable rate:

```
budget       5/1000 failed, burn rate 5.0x
             a 30-day budget gone in 6 days
```

That is a more useful sentence than "0.5% error rate". It has a deadline in it.
Nobody has to decide whether half a percent is bad; the budget already encodes
that decision, made once, when the objective was set. Six days is either fine
because a fix is shipping tomorrow, or it is not, and either way the conversation
is about time rather than about whether the number feels high.

## Two windows, because one is a rumour

A short window alone brings back the alert nobody trusts. Any blip clears it.

The chapter's answer is to require two windows to agree: a short one saying the
burn is happening now, and a long one saying it has been happening for a while.

```go
spike, _ := slo.EvaluateBurnRateAlert(slo.BurnRateAlertOptions{
    ShortWindow:    window(14.4), // burning fast right now
    LongWindow:     window(0.5),  // but the hour looks fine
    ShortThreshold: 14.0,
    LongThreshold:  6.0,
})
```

```
two windows  spike: long-window-below-threshold (silent)
             sustained: burn-rate-thresholds-exceeded (pages)
```

The first case is the 3am spike. Something genuinely burned fast for a moment,
the short window noticed, and the long window declined to care. Nobody is woken.
The second is the same short-window reading with an hour of history behind it,
and that pages.

The decision carries a reason rather than a bare boolean, which matters at 3am.
`long-window-below-threshold` tells you the alert was evaluated and deliberately
stayed quiet — a different thing from an alert that never ran.

## The projection

Both of those look backwards. Chapter 12's real contribution looks forward.

The authors implement it in Go: take the recent baseline, project it across the
window you care about, and fire only if the budget would actually be exhausted
inside it. Their `isBurnViolation` is a self-contained calculation — you hand it a
bucketed timeseries and it hands you back a bool.

The scenario that makes it earn its keep is the one where nothing looks wrong. A
month of healthy traffic, then a deploy. Most of the budget is still there. A
threshold alert sees a rate of half a percent and stays quiet, correctly, because
half a percent is not an outage.

```
projection   baseline 0.5% failures over 6h0m0s
             budget exhausted in 9h43m, inside the 24h0m0s window
             reason: projected-budget-exhaustion
```

Nothing is on fire. The budget still has room. And you have under ten hours before
it does not, which is a thing worth knowing during working days rather than at
the moment it runs out.

## One constraint worth keeping

The chapter caps extrapolation at four times the baseline window — `lookbackRatio
= 4` in the authors' code. We enforce the same limit, which we discovered by
asking for a 24-hour projection from ten minutes of evidence and being told no:

```
slo: lookahead must not exceed 4 times baseline
```

That refusal is the feature. Ten minutes of traffic projected across a day is not
a forecast, it is a rumour with a decimal point on it, and an alert built from one
is the threshold alert again wearing a better hat. A 24-hour projection needs six
hours behind it.

## Testing something that takes six days

None of the numbers above were waited for. The tracker takes an injected clock:

```go
tracker, _ := slo.NewTracker(definition, slo.WithClock(clock.Now))
```

so six days of budget burn and a 24-hour projection are computed exactly, in
milliseconds, in CI. A forecast you cannot test is a forecast you will not trust,
and a forecast you cannot test *quickly* is one you will stop running.

---

The example: [`examples/book/ch12-burn-alerts`](../../examples/book/ch12-burn-alerts).
The book's version: `2e/chapter-12-slo-based-alerts` in the
[companion repository](https://github.com/oreillymedia/observability_engineering).
