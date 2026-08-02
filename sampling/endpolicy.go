package sampling

import (
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

// KeepSpan decides whether an ended span survives.
//
// The decision is derived from the trace ID, so every span in a trace resolves
// the baseline identically and routine traces are kept or dropped whole. A span
// that failed or ran slow is kept on its own account, which can leave it as the
// only surviving span of an otherwise dropped trace. That is the same trade the
// book's own head-and-tail example makes: a tail decision cannot be propagated
// back to spans that already ended.
//
// ponytail: per-span decision, no trace buffering. Keeping the whole trace around
// an error means holding spans until the root ends, with the memory bound and
// eviction policy that implies. Add that if partial traces prove to be the thing
// people actually hit.
func (p EndPolicy) KeepSpan(s sdktrace.ReadOnlySpan) bool {
	traceID := s.SpanContext().TraceID()

	if s.Status().Code == codes.Error {
		return keepAtRate(traceID, p.ErrorRate)
	}
	if p.SlowThreshold > 0 && s.EndTime().Sub(s.StartTime()) >= p.SlowThreshold {
		return keepAtRate(traceID, p.SlowRate)
	}
	return keepAtRate(traceID, p.BaselineRate)
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
