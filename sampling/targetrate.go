package sampling

import (
	"sort"
	"strings"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TargetRateSampler keeps a target number of traces per second rather than a
// fixed fraction of them.
//
// A fixed rate has to be re-tuned by hand every time traffic moves: the rate that
// kept a sensible trickle at midnight floods the pipeline at midday, and the rate
// that survives midday sees almost nothing overnight. This measures the volume it
// is actually receiving and recomputes the keep rate to land on the budget you
// asked for.
//
// Give it a key function and the budget is enforced per key instead, so a single
// noisy endpoint cannot spend the whole allowance, and a rare one is not crowded
// out. That is the difference between a service where every error storm looks the
// same and one where you still see the quiet failures underneath it.
//
// Decisions are derived from the trace ID, so a trace is kept or dropped whole.
//
// The rate in force during a window is computed from the window before it, so a
// change in traffic takes one interval to be reflected. A sudden spike is sampled
// at the old, more generous rate until the next adjustment; shorten the interval
// to react faster, at the cost of noisier rates on low traffic.
type TargetRateSampler struct {
	perSecond float64
	interval  time.Duration
	key       func(sdktrace.SamplingParameters) string
	clock     func() time.Time
	maxKeys   int

	mu           sync.Mutex
	counts       map[string]int64
	rates        map[string]float64
	windowOpened time.Time
}

// TargetRateOption configures a TargetRateSampler.
type TargetRateOption func(*TargetRateSampler)

// WithTargetSpansPerSecond sets the budget: how many traces per second, per key,
// the sampler should aim to keep.
func WithTargetSpansPerSecond(perSecond float64) TargetRateOption {
	return func(s *TargetRateSampler) {
		if perSecond > 0 {
			s.perSecond = perSecond
		}
	}
}

// WithAdjustInterval sets how often the rate is recomputed from observed volume.
// Shorter reacts faster and is noisier on low traffic; the default is one minute,
// as in chapter 15 of Observability Engineering.
func WithAdjustInterval(interval time.Duration) TargetRateOption {
	return func(s *TargetRateSampler) {
		if interval > 0 {
			s.interval = interval
		}
	}
}

// WithSamplingKey groups traffic into buckets that each get their own budget.
// Without one, the whole service shares a single budget.
func WithSamplingKey(key func(sdktrace.SamplingParameters) string) TargetRateOption {
	return func(s *TargetRateSampler) {
		if key != nil {
			s.key = key
		}
	}
}

// WithMaxSamplingKeys bounds how many distinct keys are tracked. Keys are dropped
// least-frequent-first when the limit is hit, so an unbounded key — a raw URL, an
// error string with an ID in it — degrades into inaccuracy rather than into
// memory exhaustion.
func WithMaxSamplingKeys(max int) TargetRateOption {
	return func(s *TargetRateSampler) {
		if max > 0 {
			s.maxKeys = max
		}
	}
}

// WithTargetRateClock replaces the clock, so a test does not have to wait a
// minute to observe an adjustment.
func WithTargetRateClock(clock func() time.Time) TargetRateOption {
	return func(s *TargetRateSampler) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// KeyByAttributes builds a sampling key from span attributes, which is how a
// per-key budget is usually wanted: by endpoint, by status, by shard.
// Attributes missing from a span contribute an empty component, so spans are
// grouped consistently whether or not every attribute is present.
func KeyByAttributes(keys ...string) func(sdktrace.SamplingParameters) string {
	return func(p sdktrace.SamplingParameters) string {
		parts := make([]string, 0, len(keys))
		for _, want := range keys {
			value := ""
			for _, attr := range p.Attributes {
				if string(attr.Key) == want {
					value = attr.Value.String()
					break
				}
			}
			parts = append(parts, value)
		}
		return strings.Join(parts, "\x00")
	}
}

// NewTargetRateSampler creates a sampler that holds a traces-per-second budget.
//
// Until the first interval has elapsed there is no measured volume to divide, so
// it keeps everything. A sampler that guessed a rate before it had evidence would
// spend its first interval wrong in whichever direction the guess missed.
func NewTargetRateSampler(opts ...TargetRateOption) *TargetRateSampler {
	s := &TargetRateSampler{
		perSecond: 1,
		interval:  time.Minute,
		key:       func(sdktrace.SamplingParameters) string { return "" },
		clock:     time.Now,
		maxKeys:   1000,
		counts:    make(map[string]int64),
		rates:     make(map[string]float64),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.windowOpened = s.clock()
	return s
}

// ShouldSample implements trace.Sampler.
func (s *TargetRateSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := oteltrace.SpanContextFromContext(p.ParentContext)
	if psc.IsSampled() {
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample, Tracestate: psc.TraceState()}
	}

	key := s.key(p)

	s.mu.Lock()
	if now := s.clock(); now.Sub(s.windowOpened) >= s.interval {
		s.recompute(now)
	}
	s.counts[key]++
	rate, known := s.rates[key]
	s.mu.Unlock()

	if !known {
		rate = 1.0 // nothing measured yet
	}
	if keepAtRate(p.TraceID, rate) {
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
	}
	return sdktrace.SamplingResult{Decision: sdktrace.Drop}
}

// recompute turns the volume observed over the last window into the keep rate
// for the next one. The caller holds the lock.
func (s *TargetRateSampler) recompute(now time.Time) {
	elapsed := now.Sub(s.windowOpened).Seconds()
	if elapsed <= 0 {
		return
	}

	for key, count := range s.counts {
		observedPerSecond := float64(count) / elapsed
		if observedPerSecond <= s.perSecond {
			s.rates[key] = 1.0 // under budget: keep it all
			continue
		}
		s.rates[key] = s.perSecond / observedPerSecond
	}

	// Keys absent from the last window are no longer worth a rate.
	for key := range s.rates {
		if _, seen := s.counts[key]; !seen {
			delete(s.rates, key)
		}
	}

	s.trimKeys()
	s.counts = make(map[string]int64, len(s.counts))
	s.windowOpened = now
}

// trimKeys drops the least-frequent keys once the tracked set exceeds the limit.
// The caller holds the lock.
func (s *TargetRateSampler) trimKeys() {
	if len(s.counts) <= s.maxKeys {
		return
	}

	type keyCount struct {
		key   string
		count int64
	}
	ordered := make([]keyCount, 0, len(s.counts))
	for key, count := range s.counts {
		ordered = append(ordered, keyCount{key, count})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].count > ordered[j].count })

	for _, entry := range ordered[s.maxKeys:] {
		delete(s.counts, entry.key)
		delete(s.rates, entry.key)
	}
}

// Description implements trace.Sampler.
func (s *TargetRateSampler) Description() string {
	return "TargetRateSampler"
}
