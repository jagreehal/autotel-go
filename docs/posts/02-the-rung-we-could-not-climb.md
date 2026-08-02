# Writing chapter 15 as code found a bug that had been shipping for months

Chapter 15 of _Observability Engineering_ presents sampling as a ladder. Nine
rungs, each one fixing something the rung below it broke. Keep everything, and
the bill arrives. Keep one in a thousand at random, and every count you run is
now wrong. Record the rate alongside the event, and counts become recoverable.
Derive the decision from a propagated ID, and traces stop arriving with holes in
them. And so on, up to head-and-tail sampling at the top.

We set out to write that ladder as a running program against our own library, one
rung at a time, asserting each claim rather than describing it. We got to rung 6
and the library failed.

## Rung 6

Rung 6 is the first genuinely useful one. Keep routine traffic at some low rate,
and keep the outliers — errors, slow requests — far more aggressively. It is the
policy almost everyone actually wants, because it is the one that makes a
sampled service still debuggable.

Our library appeared to support it. The README said so:

```go
autotel.WithAdaptiveSampler(
    sampling.WithBaselineRate(0.1), // 10% baseline
    sampling.WithErrorRate(1.0),    // 100% errors
)
```

Writing the example meant asserting it, and the assertion is obvious: set the
baseline to zero, record a failure, and check the failure survives. It did not.
Nothing survived.

```
WithErrorRate(1.0) kept 0/256 error spans; every error should survive
```

`WithErrorRate` set a field. `WithSlowThreshold` set a field. `WithSlowRate` set
a field. And `ShouldSample` — the function that decides — never read any of them.
It consulted the parent decision, then links, then the baseline rate, and
returned. Three public options, documented in the README, doing nothing at all.

Every service configured the way our own documentation recommended was dropping
ninety percent of its errors and had no way to know.

## Why the tests did not catch it

There was a test. This one:

```go
func TestAdaptiveSampler_Options(t *testing.T) {
	sampler := NewAdaptiveSampler(
		WithBaselineRate(0.5),
		WithErrorRate(0.8),
		WithSlowThreshold(2e9),
		WithSlowRate(0.9),
	)
	require.NotNil(t, sampler)
	result := sampler.ShouldSample(...)
	assert.NotNil(t, result)
}
```

It constructs a sampler with all four options and asserts the result is not nil.
A sampler that ignores every option passes it. A sampler that returns a constant
passes it. It tests that the code does not panic, which was never in doubt, and
nothing else.

This is the failure mode worth naming: a test that asserts a value was *set*
rather than that something *happened*. The option assigned its field correctly.
The field was simply never read again. No test that stops at the constructor can
see that gap, and both of the dead features this library has shipped — the other
was a span filter silently discarded by the config merge — hid in exactly that
blind spot.

## Why it could not be fixed where it was configured

The obvious repair is to make `ShouldSample` read the fields. That does not work,
and the reason is worth sitting with.

A head sampler runs when a span *starts*. At that moment the span has no status,
because nothing has failed yet, and no duration, because no time has passed.
"Keep every error" is not a decision a head sampler is capable of making. Neither
is "keep everything slower than a second". The options were not merely unwired;
they were configured at a layer that could never honour them.

So the rates moved to where the facts are. They are applied at `OnEnd`, by a span
processor, at the point where a span knows whether it failed and how long it
took. `Init` reads the sampler's configuration and wires both halves.

That has a consequence you should know about before you upgrade. A span dropped
at head never reaches `OnEnd`, so it can never be kept for having failed. To keep
errors, the head has to record everything and let the tail do the dropping. Export
volume is unchanged — it still follows the rates you set — but spans are now built
in the process before being discarded locally. That is the cost of tail sampling,
and it is not free. Set the error and slow rates no higher than the baseline and
you get the old head-only behaviour back.

## Then the fix broke traces

We pointed the fixed library at a local OTLP receiver to see it work. A two-span
trace, a parent and a child, with the child failing. One span arrived.

The error was kept. Its parent was routine, so the baseline dropped it — and the
receiver, given a single span whose parent had never arrived, displayed that span
as the root of the trace.

Chapter 15 warns about precisely this, several rungs earlier: sampling that puts
holes in the waterfall means the trace you open is missing the part you came for.
We had fixed "errors get dropped" and replaced it with "errors arrive with no
context around them", which is not obviously better.

The repair uses an ordering fact. A child span always ends before its parent. So
when a span is kept for failing, mark its trace; any span that ends afterwards —
which is every ancestor — is kept along with it. A sibling that ended before the
failure is already gone and cannot be recovered, because a tail decision cannot
travel backwards. But the chain from the error up to the root arrives intact, and
that is what makes a kept error readable.

The marks are bounded to ten thousand traces and expire after five minutes, so a
long-lived process does not slowly accumulate every trace ID it has ever seen.

## The rungs we could not climb at all

With rung 6 working we kept going, and stopped again at rungs 5, 7 and 8.

Rung 5 is target-rate sampling: rather than tuning a fixed rate by hand as traffic
rises and falls, measure the volume you are receiving and recompute the rate to
hit a budget. Rungs 7 and 8 extend that per key, so a noisy endpoint cannot spend
the allowance a rare one needs, with the keys discovered from traffic rather than
enumerated in a switch statement.

Our library could not do any of it. The type was called `AdaptiveSampler` and its
rates were static; nothing adapted to anything. The first version of the example
said so, in the output, in the same list as the rungs that worked:

```
Not covered, and worth saying plainly:
  5 target rate        needs a feedback loop that recomputes the rate
                       from observed volume; autotel's rates are static
```

An example that quietly skipped the rungs we failed would have been advertising.
Naming them made the gap concrete enough to close, and we built the sampler. All
nine rungs now run:

```
  5 target rate        600/6000 under load, 600/600 when quiet
                       same config, no rate re-tuned by hand
  7 key + target rate  /health 600/6000, /checkout 300/300
  8 dynamic many keys  the noisy route cannot spend the quiet one's budget
```

It measures over an interval and applies the resulting rate to the next one,
which means a traffic change takes one interval to show up. That is inherent to
measuring before deciding, and it is documented rather than hidden.

## The actual argument

The book's central claim is that you cannot reason about a system from what you
assumed it does. You have to ask it.

A library is a system. We assumed ours kept errors, because the README said so
and a test was green. It did not, and had not for months. What surfaced it was
not a code review or a bug report. It was writing down what chapter 15 said would
be true and running it.

The examples live in CI now. If the sampling regresses, chapter 15 stops working
and the build goes red. Prose about a library cannot fail; it just goes quietly
stale, and keeps making a promise the code stopped keeping.

---

The example: [`examples/book/ch15-sampling`](../../examples/book/ch15-sampling).
The book's nine programs: `2e/chapter-15-sampling` in the
[companion repository](https://github.com/oreillymedia/observability_engineering).
