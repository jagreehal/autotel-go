// Package webhook provides the "Parking Lot" pattern for async callback tracing.
//
// When initiating async operations that return hours/days later (webhooks,
// payment callbacks, human approvals), you can't keep a span open. This package
// provides utilities to "park" trace context and retrieve it when callbacks arrive.
//
// Example:
//
//	store := webhook.NewInMemoryStore()
//	lot := webhook.NewParkingLot(store, webhook.WithDefaultTTL(24*time.Hour))
//
//	// When initiating payment
//	func initiatePayment(ctx context.Context, orderID string) error {
//	    ctx, span := tracer.Start(ctx, "initiate-payment")
//	    defer span.End()
//
//	    // Park the trace context before making async call
//	    err := lot.Park(ctx, "payment:"+orderID, webhook.WithMetadata(map[string]string{
//	        "order_id": orderID,
//	    }))
//	    if err != nil {
//	        return err
//	    }
//
//	    return stripe.CreatePaymentIntent(orderID)
//	}
//
//	// When Stripe webhook arrives (hours later)
//	func handleWebhook(ctx context.Context, event StripeEvent) error {
//	    orderID := event.Data.Object.Metadata["order_id"]
//
//	    // Retrieve parked context and create linked span
//	    ctx, span, err := lot.RetrieveAndTrace(ctx, "payment:"+orderID, "stripe.webhook.payment_intent.succeeded")
//	    if err != nil {
//	        // Context not found - log but continue
//	    }
//	    defer span.End()
//
//	    return fulfillOrder(ctx, orderID)
//	}
package webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// StoredContext represents a parked trace context.
type StoredContext struct {
	TraceID    string            // Hex-encoded trace ID
	SpanID     string            // Hex-encoded span ID
	TraceFlags byte              // Sampling flags
	ParkedAt   time.Time         // When the context was parked
	TTL        time.Duration     // How long to keep the context
	Metadata   map[string]string // User-provided metadata
}

// IsExpired checks if the stored context has expired.
func (sc *StoredContext) IsExpired() bool {
	if sc.TTL == 0 {
		return false
	}
	return time.Since(sc.ParkedAt) > sc.TTL
}

// ElapsedMs returns milliseconds since the context was parked.
func (sc *StoredContext) ElapsedMs() int64 {
	return time.Since(sc.ParkedAt).Milliseconds()
}

// Store is the interface for trace context storage backends.
// Implement this interface for different storage backends (Redis, DynamoDB, etc.)
type Store interface {
	// Save stores trace context with a correlation key.
	Save(ctx context.Context, key string, sc *StoredContext) error

	// Load retrieves trace context by correlation key.
	// Returns nil if not found or expired.
	Load(ctx context.Context, key string) (*StoredContext, error)

	// Delete removes trace context by correlation key.
	Delete(ctx context.Context, key string) error

	// Exists checks if a key exists without loading.
	Exists(ctx context.Context, key string) (bool, error)
}

// InMemoryStore is a simple in-memory implementation of Store.
// Use for testing/development. For production, use Redis or DynamoDB.
type InMemoryStore struct {
	mu              sync.RWMutex
	data            map[string]*StoredContext
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

// InMemoryStoreOption configures the in-memory store.
type InMemoryStoreOption func(*InMemoryStore)

// WithCleanupInterval sets the cleanup interval for expired entries.
func WithCleanupInterval(d time.Duration) InMemoryStoreOption {
	return func(s *InMemoryStore) {
		s.cleanupInterval = d
	}
}

// NewInMemoryStore creates a new in-memory store.
func NewInMemoryStore(opts ...InMemoryStoreOption) *InMemoryStore {
	s := &InMemoryStore{
		data:            make(map[string]*StoredContext),
		cleanupInterval: time.Minute,
		stopCleanup:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	// Start cleanup goroutine
	if s.cleanupInterval > 0 {
		go s.cleanupLoop()
	}

	return s
}

func (s *InMemoryStore) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanup()
		case <-s.stopCleanup:
			return
		}
	}
}

func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, sc := range s.data {
		if sc.IsExpired() {
			delete(s.data, key)
		}
	}
}

// Save stores trace context.
func (s *InMemoryStore) Save(ctx context.Context, key string, sc *StoredContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = sc
	return nil
}

// Load retrieves trace context.
func (s *InMemoryStore) Load(ctx context.Context, key string) (*StoredContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sc, ok := s.data[key]
	if !ok {
		return nil, nil
	}

	if sc.IsExpired() {
		return nil, nil
	}

	return sc, nil
}

// Delete removes trace context.
func (s *InMemoryStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// Exists checks if a key exists.
func (s *InMemoryStore) Exists(ctx context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sc, ok := s.data[key]
	if !ok {
		return false, nil
	}

	return !sc.IsExpired(), nil
}

// Close stops the cleanup goroutine.
func (s *InMemoryStore) Close() {
	close(s.stopCleanup)
}

// Size returns the number of stored contexts (for testing).
func (s *InMemoryStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// ParkingLot manages trace context parking and retrieval.
type ParkingLot struct {
	store      Store
	keyPrefix  string
	defaultTTL time.Duration
	autoDelete bool
	tracerName string
	onMiss     func(key string)
	onPark     func(key string, sc *StoredContext)
	onRetrieve func(key string, sc *StoredContext)
}

// ParkingLotOption configures the parking lot.
type ParkingLotOption func(*ParkingLot)

// WithKeyPrefix sets a prefix for all correlation keys.
func WithKeyPrefix(prefix string) ParkingLotOption {
	return func(p *ParkingLot) {
		p.keyPrefix = prefix
	}
}

// WithDefaultTTL sets the default TTL for parked contexts.
func WithDefaultTTL(ttl time.Duration) ParkingLotOption {
	return func(p *ParkingLot) {
		p.defaultTTL = ttl
	}
}

// WithAutoDelete enables automatic deletion after retrieval.
func WithAutoDelete(enabled bool) ParkingLotOption {
	return func(p *ParkingLot) {
		p.autoDelete = enabled
	}
}

// WithTracerName sets the tracer name for callback spans.
func WithTracerName(name string) ParkingLotOption {
	return func(p *ParkingLot) {
		p.tracerName = name
	}
}

// WithOnMiss sets a callback for when context is not found.
func WithOnMiss(fn func(key string)) ParkingLotOption {
	return func(p *ParkingLot) {
		p.onMiss = fn
	}
}

// WithOnPark sets a callback when context is parked.
func WithOnPark(fn func(key string, sc *StoredContext)) ParkingLotOption {
	return func(p *ParkingLot) {
		p.onPark = fn
	}
}

// WithOnRetrieve sets a callback when context is retrieved.
func WithOnRetrieve(fn func(key string, sc *StoredContext)) ParkingLotOption {
	return func(p *ParkingLot) {
		p.onRetrieve = fn
	}
}

// NewParkingLot creates a new parking lot.
func NewParkingLot(store Store, opts ...ParkingLotOption) *ParkingLot {
	p := &ParkingLot{
		store:      store,
		keyPrefix:  "parkingLot:",
		defaultTTL: 24 * time.Hour,
		autoDelete: true,
		tracerName: "autotel/webhook",
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// ParkOption configures a single park operation.
type ParkOption func(*parkConfig)

type parkConfig struct {
	metadata map[string]string
	ttl      time.Duration
}

// WithMetadata adds metadata to the parked context.
func WithMetadata(metadata map[string]string) ParkOption {
	return func(c *parkConfig) {
		c.metadata = metadata
	}
}

// WithTTL sets a custom TTL for this park operation.
func WithTTL(ttl time.Duration) ParkOption {
	return func(c *parkConfig) {
		c.ttl = ttl
	}
}

// Park stores the current trace context for later retrieval.
// Call this before initiating an async operation (payment, webhook, etc.)
//
// Example:
//
//	err := lot.Park(ctx, "payment:order-123",
//	    webhook.WithMetadata(map[string]string{"order_id": "123"}),
//	    webhook.WithTTL(48*time.Hour),
//	)
func (p *ParkingLot) Park(ctx context.Context, correlationKey string, opts ...ParkOption) error {
	cfg := &parkConfig{
		ttl: p.defaultTTL,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	span := trace.SpanFromContext(ctx)
	spanCtx := span.SpanContext()

	sc := &StoredContext{
		TraceID:    spanCtx.TraceID().String(),
		SpanID:     spanCtx.SpanID().String(),
		TraceFlags: byte(spanCtx.TraceFlags()),
		ParkedAt:   time.Now(),
		TTL:        cfg.ttl,
		Metadata:   cfg.metadata,
	}

	fullKey := p.keyPrefix + correlationKey

	if err := p.store.Save(ctx, fullKey, sc); err != nil {
		return fmt.Errorf("failed to park trace context: %w", err)
	}

	// Record park event on current span
	if span.IsRecording() {
		eventAttrs := []attribute.KeyValue{
			attribute.String("parking_lot.correlation_key", correlationKey),
			attribute.Int64("parking_lot.ttl_seconds", int64(cfg.ttl.Seconds())),
		}
		if cfg.metadata != nil {
			for k, v := range cfg.metadata {
				eventAttrs = append(eventAttrs, attribute.String("parking_lot.metadata."+k, v))
			}
		}
		span.AddEvent("trace_context_parked", trace.WithAttributes(eventAttrs...))
	}

	if p.onPark != nil {
		p.onPark(correlationKey, sc)
	}

	return nil
}

// Retrieve loads a parked context without creating a span.
// Returns nil if not found or expired.
func (p *ParkingLot) Retrieve(ctx context.Context, correlationKey string) (*StoredContext, error) {
	fullKey := p.keyPrefix + correlationKey

	sc, err := p.store.Load(ctx, fullKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load trace context: %w", err)
	}

	if sc == nil {
		if p.onMiss != nil {
			p.onMiss(correlationKey)
		}
		return nil, nil
	}

	if p.autoDelete {
		_ = p.store.Delete(ctx, fullKey)
	}

	if p.onRetrieve != nil {
		p.onRetrieve(correlationKey, sc)
	}

	return sc, nil
}

// RetrieveAndTrace retrieves parked context and creates a linked span.
// This is the primary method for handling callbacks.
// Returns the stored context (may be nil if not found), the new span, and any error.
//
// Example:
//
//	ctx, span, _ := lot.RetrieveAndTrace(ctx, "payment:order-123", "stripe.webhook.payment_succeeded")
//	defer span.End()
func (p *ParkingLot) RetrieveAndTrace(ctx context.Context, correlationKey string, spanName string) (context.Context, trace.Span, *StoredContext) {
	sc, _ := p.Retrieve(ctx, correlationKey)

	tracer := otel.Tracer(p.tracerName)

	var spanOpts []trace.SpanStartOption
	spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindServer))

	// Create link to original span if we have parked context
	if sc != nil {
		link := p.createLink(sc)
		spanOpts = append(spanOpts, trace.WithLinks(link))
	}

	ctx, span := tracer.Start(ctx, spanName, spanOpts...)

	// Set attributes
	span.SetAttributes(attribute.String("parking_lot.correlation_key", correlationKey))

	if sc != nil {
		span.SetAttributes(
			attribute.Int64("parking_lot.elapsed_ms", sc.ElapsedMs()),
			attribute.String("parking_lot.original_trace_id", sc.TraceID),
			attribute.String("parking_lot.original_span_id", sc.SpanID),
		)

		if sc.Metadata != nil {
			for k, v := range sc.Metadata {
				span.SetAttributes(attribute.String("parking_lot.metadata."+k, v))
			}
		}

		span.AddEvent("parked_context_retrieved", trace.WithAttributes(
			attribute.String("parking_lot.correlation_key", correlationKey),
			attribute.Int64("parking_lot.elapsed_ms", sc.ElapsedMs()),
			attribute.String("parking_lot.original_trace_id", sc.TraceID),
		))
	} else {
		span.SetAttributes(attribute.Bool("parking_lot.context_found", false))
	}

	return ctx, span, sc
}

// createLink creates a span link from stored context.
func (p *ParkingLot) createLink(sc *StoredContext) trace.Link {
	traceID, _ := trace.TraceIDFromHex(sc.TraceID)
	spanID, _ := trace.SpanIDFromHex(sc.SpanID)

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(sc.TraceFlags),
		Remote:     true,
	})

	return trace.Link{
		SpanContext: spanCtx,
		Attributes: []attribute.KeyValue{
			attribute.String("link.type", "parking_lot"),
			attribute.Int64("parking_lot.parked_at", sc.ParkedAt.UnixMilli()),
		},
	}
}

// Exists checks if a parked context exists (without retrieving/deleting).
func (p *ParkingLot) Exists(ctx context.Context, correlationKey string) (bool, error) {
	fullKey := p.keyPrefix + correlationKey
	return p.store.Exists(ctx, fullKey)
}

// Delete explicitly removes a parked context.
func (p *ParkingLot) Delete(ctx context.Context, correlationKey string) error {
	fullKey := p.keyPrefix + correlationKey
	return p.store.Delete(ctx, fullKey)
}

// CreateCorrelationKey creates a correlation key from multiple parts.
//
// Example:
//
//	key := webhook.CreateCorrelationKey("payment", orderID, "stripe")
//	// Returns: "payment:order-123:stripe"
func CreateCorrelationKey(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ":" + parts[i]
	}
	return result
}
