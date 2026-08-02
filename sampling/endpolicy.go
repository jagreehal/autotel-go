package sampling

import (
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// EndPolicy holds the sampling decisions that cannot be made when a span starts,
// because they depend on how it finished: whether it failed, and how long it took.
//
// A head sampler runs at span start, where neither fact exists yet. Keeping every
// error is therefore not a head-sampling decision at all, which is why the error
// and latency rates are applied by a span processor at OnEnd rather than by
// AdaptiveSampler.ShouldSample. Configure it through the AdaptiveSampler options
// and autotel.Init wires both halves; see NewAdaptiveSampler.
type EndPolicy struct {
	// BaselineRate is the keep rate for spans that neither failed nor ran slow.
	BaselineRate float64
	// ErrorRate is the keep rate for spans whose status is Error.
	ErrorRate float64
	// SlowThreshold is the duration at or above which a span counts as slow.
	// Zero disables latency-based keeping.
	SlowThreshold time.Duration
	// SlowRate is the keep rate for spans at or above SlowThreshold.
	SlowRate float64
}

// Active reports whether the policy keeps anything the baseline alone would drop.
// When it does not, there is nothing for the tail to decide and the baseline can
// be applied at head, which is cheaper: dropped spans are never recorded at all.
func (p EndPolicy) Active() bool {
	if p.ErrorRate > p.BaselineRate {
		return true
	}
	return p.SlowThreshold > 0 && p.SlowRate > p.BaselineRate
}

// NewKeeper returns the stateful decision function for this policy.
//
// The state is what stops a kept error from arriving as an orphan. Keeping a
// failed span on its own account is easy; keeping it useful means keeping the
// spans it hangs off, and "was anything in this trace kept?" is a question a
// single span cannot answer.
func (p EndPolicy) NewKeeper() *Keeper {
	return &Keeper{policy: p, kept: make(map[oteltrace.TraceID]time.Time)}
}

// Keeper applies an EndPolicy across the spans of a trace.
type Keeper struct {
	policy EndPolicy

	mu   sync.Mutex
	kept map[oteltrace.TraceID]time.Time
}

// keptTraceLimit bounds the sticky set, and keptTraceTTL bounds how long a trace
// stays sticky. A trace whose spans span more than the TTL loses the connection,
// which beats holding every trace ID a long-lived process ever saw.
const (
	keptTraceLimit = 10_000
	keptTraceTTL   = 5 * time.Minute
)

// KeepSpan decides whether an ended span survives.
//
// Routine spans resolve the baseline from the trace ID, so a trace is kept or
// dropped whole rather than arriving with holes in it. A span that failed or ran
// slow is kept on its own account, and its trace is then marked so the spans that
// end after it — its parents, since a child always ends first — are kept with it.
// That is what makes a kept error readable: an error span whose ancestors were
// dropped is a waterfall with the middle missing, which is the failure chapter 15
// of Observability Engineering warns about.
//
// A sibling that ended before the error is already gone and cannot be recovered;
// a tail decision cannot travel backwards.
//
// One mutex covers the whole process. Per-span locking on OnEnd is fine at
// ordinary span rates; shard by trace ID if it ever shows up in a profile.
func (k *Keeper) KeepSpan(s sdktrace.ReadOnlySpan) bool {
	traceID := s.SpanContext().TraceID()

	interesting := s.Status().Code == codes.Error
	rate := k.policy.ErrorRate
	if !interesting && k.policy.SlowThreshold > 0 && s.EndTime().Sub(s.StartTime()) >= k.policy.SlowThreshold {
		interesting = true
		rate = k.policy.SlowRate
	}

	if interesting {
		if !keepAtRate(traceID, rate) {
			return false
		}
		k.markKept(traceID, s.EndTime())
		return true
	}

	if k.wasKept(traceID) {
		return true
	}
	return keepAtRate(traceID, k.policy.BaselineRate)
}

func (k *Keeper) markKept(traceID oteltrace.TraceID, at time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if len(k.kept) >= keptTraceLimit {
		k.evict(at)
	}
	k.kept[traceID] = at
}

func (k *Keeper) wasKept(traceID oteltrace.TraceID) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	at, ok := k.kept[traceID]
	if !ok {
		return false
	}
	return time.Since(at) < keptTraceTTL
}

// evict drops expired entries, and if that was not enough, the oldest half. The
// caller holds the lock.
func (k *Keeper) evict(now time.Time) {
	for id, at := range k.kept {
		if now.Sub(at) >= keptTraceTTL {
			delete(k.kept, id)
		}
	}
	if len(k.kept) < keptTraceLimit {
		return
	}
	target := len(k.kept) / 2
	for id := range k.kept {
		if len(k.kept) <= target {
			return
		}
		delete(k.kept, id)
	}
}

// keepAtRate makes a deterministic keep decision from the trace ID, so that the
// same trace resolves the same way in every span and in every process that sees it.
func keepAtRate(traceID oteltrace.TraceID, rate float64) bool {
	if rate >= 1.0 {
		return true
	}
	if rate <= 0.0 {
		return false
	}
	return traceID[15] < uint8(rate*256)
}
