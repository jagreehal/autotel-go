package subscribers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/debugutil"
)

// Queue manages async event delivery to subscribers.
// It uses subscribers to send events to various destinations.
type Queue struct {
	mu          sync.RWMutex
	events      chan queuedEvent
	subscribers []Subscriber
	wg          sync.WaitGroup
	closed      bool
	cbThreshold int
	flushTick   *time.Ticker
	workerErrs  map[string]int
	nextAllowed map[string]time.Time
	backoff     map[string]time.Duration
	backoffMin  time.Duration
	backoffMax  time.Duration
	cbReset     time.Duration
}

// queuedEvent represents an event queued for delivery.
type queuedEvent struct {
	name       string
	properties map[string]any
	operation  string
}

// QueueConfig tunes queue behavior.
type QueueConfig struct {
	QueueSize        int
	FlushInterval    time.Duration
	CircuitThreshold int
	BackoffMin       time.Duration
	BackoffMax       time.Duration
	CircuitReset     time.Duration
}

// NewQueue creates a new event queue with the given subscribers.
// The queue starts processing events asynchronously.
//
// Example:
//
//	queue := subscribers.NewQueue(
//	    subscribers.NewPostHogSubscriber("phc_..."),
//	)
//	defer queue.Shutdown(context.Background())
func NewQueueWithConfig(cfg QueueConfig, subscribers ...Subscriber) *Queue {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if cfg.CircuitThreshold <= 0 {
		cfg.CircuitThreshold = 5
	}
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = 100 * time.Millisecond
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 5 * time.Second
	}
	if cfg.CircuitReset <= 0 {
		cfg.CircuitReset = 10 * time.Second
	}

	q := &Queue{
		events:      make(chan queuedEvent, cfg.QueueSize),
		subscribers: subscribers,
		cbThreshold: cfg.CircuitThreshold,
		flushTick:   time.NewTicker(cfg.FlushInterval),
		workerErrs:  make(map[string]int),
		nextAllowed: make(map[string]time.Time),
		backoff:     make(map[string]time.Duration),
		backoffMin:  cfg.BackoffMin,
		backoffMax:  cfg.BackoffMax,
		cbReset:     cfg.CircuitReset,
	}
	q.start()
	return q
}

// NewQueue creates a new event queue with default tuning.
func NewQueue(subscribers ...Subscriber) *Queue {
	return NewQueueWithConfig(QueueConfig{}, subscribers...)
}

// Track sends an event asynchronously.
// It extracts trace context from the provided context and enriches the event properties.
// If the queue is full, the event is dropped (non-blocking).
//
// Auto-enriches properties with:
//   - trace_id (32 hex chars)
//   - span_id (16 hex chars)
//
// Example:
//
//	queue.Track(ctx, "user_signed_up", map[string]any{
//	    "user_id": userID,
//	    "plan":    "premium",
//	})
func (q *Queue) Track(ctx context.Context, name string, properties map[string]any) {
	// Create a copy of properties to avoid mutating the original
	props := make(map[string]any)
	for k, v := range properties {
		props[k] = v
	}

	// Auto-enrich with telemetry context
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		sc := span.SpanContext()
		if sc.IsValid() {
			// Format as hex strings (matching Python format)
			props["trace_id"] = fmt.Sprintf("%032x", sc.TraceID())
			props["span_id"] = fmt.Sprintf("%016x", sc.SpanID())
		}
	}

	opName := autotel.GetOperationName(ctx)
	if opName != "" {
		props["operation.name"] = opName
	}

	// Non-blocking send
	select {
	case q.events <- queuedEvent{name: name, properties: props, operation: opName}:
	default:
		debugutil.Print("event queue full, dropping event=%s", name)
	}
}

func (q *Queue) start() {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		ctx := context.Background()
		for {
			select {
			case event, ok := <-q.events:
				if !ok {
					return
				}
				q.deliver(ctx, event)
			case <-q.flushTick.C:
				q.resetIfNeeded()
			}
		}
	}()
}

func (q *Queue) deliver(ctx context.Context, event queuedEvent) {
	q.mu.RLock()
	subs := q.subscribers
	q.mu.RUnlock()

	for _, sub := range subs {
		id := fmt.Sprintf("%T", sub)
		now := time.Now()
		q.mu.RLock()
		next := q.nextAllowed[id]
		q.mu.RUnlock()
		if !next.IsZero() && now.Before(next) {
			continue
		}
		err := sub.Send(ctx, event.name, event.properties)
		if err != nil {
			debugutil.Print("subscriber error: %s: %v", id, err)
			q.mu.Lock()
			q.workerErrs[id]++
			errCount := q.workerErrs[id]
			bo := q.backoff[id]
			if bo == 0 {
				bo = q.backoffMin
			}
			if bo > q.backoffMax {
				bo = q.backoffMax
			}
			nextWait := now.Add(bo)
			q.nextAllowed[id] = nextWait
			q.backoff[id] = minDuration(bo*2, q.backoffMax)
			q.mu.Unlock()

			if errCount >= q.cbThreshold {
				debugutil.Print("circuit open for subscriber=%s after %d errors, backoff=%s", id, errCount, bo)
				continue
			}
		}
		if err == nil {
			q.mu.Lock()
			q.workerErrs[id] = 0
			q.backoff[id] = q.backoffMin
			q.nextAllowed[id] = time.Time{}
			q.mu.Unlock()
		}
	}
}

// Shutdown flushes pending events and stops the queue.
// It waits for all pending events to be processed before returning.
func (q *Queue) Shutdown(ctx context.Context) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	q.mu.Unlock()

	close(q.events)
	if q.flushTick != nil {
		q.flushTick.Stop()
	}
	q.wg.Wait()

	// Close all subscribers
	q.mu.RLock()
	subs := q.subscribers
	q.mu.RUnlock()

	for _, sub := range subs {
		_ = sub.Close()
	}

	return nil
}

func (q *Queue) resetIfNeeded() {
	if q.cbReset <= 0 {
		return
	}
	q.mu.Lock()
	for id := range q.workerErrs {
		q.workerErrs[id] = 0
		q.nextAllowed[id] = time.Time{}
		q.backoff[id] = q.backoffMin
	}
	q.mu.Unlock()
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
