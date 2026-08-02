// Package workflow provides saga/workflow tracing for distributed transactions.
//
// Workflows group related operations under a single trace with step-level
// granularity. When a step fails, compensation handlers can be executed
// to rollback previous steps (saga pattern).
//
// Example:
//
//	wf := workflow.New(ctx, "order-fulfillment")
//
//	wf.Step("validate", func(ctx context.Context, span trace.Span) error {
//	    return validateOrder(ctx, order)
//	})
//
//	wf.Step("charge", func(ctx context.Context, span trace.Span) error {
//	    return chargeCustomer(ctx, order)
//	}, workflow.WithCompensation(func(ctx context.Context, span trace.Span) error {
//	    return refundCustomer(ctx, order)
//	}))
//
//	if err := wf.Run(ctx); err != nil {
//	    // Compensations are automatically executed on failure
//	    return err
//	}
package workflow

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// State represents the workflow execution state.
type State string

const (
	StatePending      State = "pending"
	StateRunning      State = "running"
	StateCompleted    State = "completed"
	StateFailed       State = "failed"
	StateCompensating State = "compensating"
	StateCompensated  State = "compensated"
)

// CompensationMode defines when compensations are executed.
type CompensationMode int

const (
	// CompensateOnFailure runs compensations when a step fails (default).
	CompensateOnFailure CompensationMode = iota
	// CompensateManual requires explicit CompensateAll() call.
	CompensateManual
	// CompensateNever disables compensation entirely.
	CompensateNever
)

// Workflow tracks a distributed transaction as a series of steps.
type Workflow struct {
	name   string
	config *Config
	tracer trace.Tracer

	mu              sync.Mutex
	state           State
	steps           []*stepDef
	completedSteps  []*stepResult
	workflowSpan    trace.Span
	workflowContext context.Context
}

// Config holds workflow configuration.
type Config struct {
	CompensationMode CompensationMode
	TracerName       string
	ExtraAttributes  []attribute.KeyValue
}

// Option configures the workflow.
type Option func(*Config)

// WithCompensationMode sets when compensations run.
func WithCompensationMode(mode CompensationMode) Option {
	return func(c *Config) {
		c.CompensationMode = mode
	}
}

// WithTracerName sets a custom tracer name.
func WithTracerName(name string) Option {
	return func(c *Config) {
		c.TracerName = name
	}
}

// WithAttributes adds custom attributes to the workflow span.
func WithAttributes(attrs ...attribute.KeyValue) Option {
	return func(c *Config) {
		c.ExtraAttributes = append(c.ExtraAttributes, attrs...)
	}
}

func defaultConfig() *Config {
	return &Config{
		CompensationMode: CompensateOnFailure,
		TracerName:       "autotel/workflow",
	}
}

// RetryConfig configures step retry behavior.
//
// Between attempts the step sleeps for the computed backoff. The sleep is
// cancellable: if the context passed to Run is cancelled while waiting, the
// step fails with the context error rather than continuing to retry.
type RetryConfig struct {
	MaxAttempts int     // Maximum number of attempts (1 = no retry)
	BackoffMs   int64   // Base backoff in milliseconds
	Multiplier  float64 // Backoff multiplier (for exponential backoff)
	MaxBackoff  int64   // Maximum backoff in milliseconds
	Jitter      bool    // Randomise backoff in [backoff/2, backoff) to avoid thundering herds
}

// stepDef defines a step before execution.
type stepDef struct {
	name           string
	handler        func(context.Context, trace.Span) error
	compensation   func(context.Context, trace.Span) error
	attributes     []attribute.KeyValue
	linkToPrevious bool     // Link to the previous step's span
	linkTo         []string // Link to specific step(s) by name
	retry          *RetryConfig
	idempotent     bool
	description    string
}

// stepResult records the result of an executed step.
type stepResult struct {
	def      *stepDef
	err      error
	spanCtx  trace.SpanContext
	executed bool
}

// StepOption configures a step.
type StepOption func(*stepDef)

// WithCompensation registers a compensation handler for saga rollback.
// Compensations run in reverse order when the workflow fails.
func WithCompensation(fn func(context.Context, trace.Span) error) StepOption {
	return func(s *stepDef) {
		s.compensation = fn
	}
}

// WithStepAttributes adds attributes to the step span.
func WithStepAttributes(attrs ...attribute.KeyValue) StepOption {
	return func(s *stepDef) {
		s.attributes = append(s.attributes, attrs...)
	}
}

// WithLinkToPrevious creates a span link to the previous step.
// Use this to show step dependencies in the trace.
func WithLinkToPrevious() StepOption {
	return func(s *stepDef) {
		s.linkToPrevious = true
	}
}

// WithLinkTo creates span links to specific steps by name.
// Use this to show explicit dependencies between steps.
//
// Example:
//
//	wf.Step("ship", handler, workflow.WithLinkTo("validate", "charge"))
func WithLinkTo(stepNames ...string) StepOption {
	return func(s *stepDef) {
		s.linkTo = append(s.linkTo, stepNames...)
	}
}

// WithRetry configures retry behavior for the step.
//
// Example:
//
//	wf.Step("charge", handler, workflow.WithRetry(workflow.RetryConfig{
//	    MaxAttempts: 3,
//	    BackoffMs:   100,
//	    Multiplier:  2.0,
//	    MaxBackoff:  5000,
//	}))
func WithRetry(config RetryConfig) StepOption {
	return func(s *stepDef) {
		s.retry = &config
	}
}

// WithIdempotent marks the step as idempotent.
// Idempotent steps can be safely retried without side effects.
func WithIdempotent() StepOption {
	return func(s *stepDef) {
		s.idempotent = true
	}
}

// WithDescription adds a description to the step.
func WithDescription(desc string) StepOption {
	return func(s *stepDef) {
		s.description = desc
	}
}

// New creates a new workflow.
//
// The workflow span is created immediately but steps are deferred until Run().
// This allows defining all steps before execution begins.
func New(ctx context.Context, name string, opts ...Option) *Workflow {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	tracer := otel.Tracer(cfg.TracerName)

	// Create workflow-level span
	attrs := []attribute.KeyValue{
		attribute.String("workflow.name", name),
		attribute.String("workflow.state", string(StatePending)),
	}
	attrs = append(attrs, cfg.ExtraAttributes...)

	ctx, span := tracer.Start(ctx, fmt.Sprintf("workflow.%s", name),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)

	return &Workflow{
		name:            name,
		config:          cfg,
		tracer:          tracer,
		state:           StatePending,
		steps:           make([]*stepDef, 0),
		completedSteps:  make([]*stepResult, 0),
		workflowSpan:    span,
		workflowContext: ctx,
	}
}

// Step registers a step to be executed.
// Steps are executed in order when Run() is called.
func (w *Workflow) Step(name string, handler func(context.Context, trace.Span) error, opts ...StepOption) *Workflow {
	step := &stepDef{
		name:    name,
		handler: handler,
	}

	for _, opt := range opts {
		opt(step)
	}

	w.mu.Lock()
	w.steps = append(w.steps, step)
	w.mu.Unlock()

	return w
}

// Run executes all registered steps in order.
// If a step fails and CompensateOnFailure is set, compensations run automatically.
func (w *Workflow) Run(ctx context.Context) error {
	w.mu.Lock()
	if w.state != StatePending {
		w.mu.Unlock()
		return fmt.Errorf("workflow already started (state: %s)", w.state)
	}
	w.state = StateRunning
	w.mu.Unlock()

	w.workflowSpan.SetAttributes(attribute.String("workflow.state", string(StateRunning)))
	w.workflowSpan.AddEvent("workflow.started", trace.WithAttributes(
		attribute.Int("workflow.step_count", len(w.steps)),
	))

	var failedStep *stepResult
	var failedErr error

	// Steps run under the workflow span (for parenting) but inherit cancellation
	// and values from the context passed to Run.
	stepParent := trace.ContextWithSpan(ctx, w.workflowSpan)

	// Execute steps in order
	for i, step := range w.steps {
		if err := ctx.Err(); err != nil {
			failedErr = err
			w.workflowSpan.AddEvent("workflow.cancelled", trace.WithAttributes(
				attribute.Int("workflow.steps_executed", i),
			))
			break
		}

		result := w.executeStep(stepParent, i, step)

		w.mu.Lock()
		w.completedSteps = append(w.completedSteps, result)
		w.mu.Unlock()

		if result.err != nil {
			failedStep = result
			failedErr = result.err
			break
		}
	}

	if failedErr != nil {
		w.mu.Lock()
		w.state = StateFailed
		w.mu.Unlock()

		w.workflowSpan.SetAttributes(attribute.String("workflow.state", string(StateFailed)))
		w.workflowSpan.RecordError(failedErr)
		if failedStep != nil {
			w.workflowSpan.SetStatus(codes.Error, fmt.Sprintf("step '%s' failed: %s", failedStep.def.name, failedErr))
		} else {
			// No step failed: the workflow context was cancelled before a step ran.
			w.workflowSpan.SetStatus(codes.Error, failedErr.Error())
		}

		// Run compensations if configured
		if w.config.CompensationMode == CompensateOnFailure {
			compErr := w.compensate(ctx)
			if compErr != nil {
				w.workflowSpan.RecordError(compErr)
				w.workflowSpan.SetStatus(codes.Error, fmt.Sprintf("compensation failed: %s", compErr))
			}
		}

		w.workflowSpan.End()
		return failedErr
	}

	// Success
	w.mu.Lock()
	w.state = StateCompleted
	w.mu.Unlock()

	w.workflowSpan.SetAttributes(attribute.String("workflow.state", string(StateCompleted)))
	w.workflowSpan.AddEvent("workflow.completed", trace.WithAttributes(
		attribute.Int("workflow.steps_executed", len(w.completedSteps)),
	))
	w.workflowSpan.End()

	return nil
}

// executeStep runs a single step with its own span.
func (w *Workflow) executeStep(ctx context.Context, index int, step *stepDef) *stepResult {
	result := &stepResult{
		def:      step,
		executed: true,
	}

	attrs := []attribute.KeyValue{
		attribute.String("workflow.name", w.name),
		attribute.String("workflow.step.name", step.name),
		attribute.Int("workflow.step.index", index),
		attribute.Bool("workflow.step.has_compensation", step.compensation != nil),
	}

	if step.description != "" {
		attrs = append(attrs, attribute.String("workflow.step.description", step.description))
	}

	if step.idempotent {
		attrs = append(attrs, attribute.Bool("workflow.step.idempotent", true))
	}

	if step.retry != nil {
		attrs = append(attrs, attribute.Int("workflow.step.max_attempts", step.retry.MaxAttempts))
	}

	attrs = append(attrs, step.attributes...)

	// Build span options including links
	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	}

	// Add links to previous step or specific steps
	links := w.buildStepLinks(index, step)
	if len(links) > 0 {
		spanOpts = append(spanOpts, trace.WithLinks(links...))
	}

	// Create step span as child of workflow span
	stepCtx, stepSpan := w.tracer.Start(ctx, fmt.Sprintf("workflow.step.%s", step.name), spanOpts...)
	defer stepSpan.End()

	result.spanCtx = stepSpan.SpanContext()

	stepSpan.AddEvent("step.started")

	// Execute with retry if configured
	var err error
	maxAttempts := 1
	if step.retry != nil && step.retry.MaxAttempts > 1 {
		maxAttempts = step.retry.MaxAttempts
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// Retry attempt: wait out the backoff before trying again.
			backoff := w.calculateBackoff(step.retry, attempt)
			stepSpan.AddEvent("step.retry_scheduled", trace.WithAttributes(
				attribute.Int("workflow.step.retry_attempt", attempt),
				attribute.Int("workflow.step.max_attempts", maxAttempts),
				attribute.Int64("workflow.step.backoff_ms", backoff),
			))

			if waitErr := sleepContext(stepCtx, time.Duration(backoff)*time.Millisecond); waitErr != nil {
				stepSpan.AddEvent("step.retry_abandoned", trace.WithAttributes(
					attribute.Int("workflow.step.retry_attempt", attempt),
					attribute.String("error.message", waitErr.Error()),
				))
				err = waitErr
				break
			}

			stepSpan.SetAttributes(attribute.Int("workflow.step.retry_attempt", attempt))
		}

		err = step.handler(stepCtx, stepSpan)
		if err == nil {
			break // Success
		}

		if attempt < maxAttempts {
			stepSpan.AddEvent("step.retry_failed", trace.WithAttributes(
				attribute.Int("workflow.step.retry_attempt", attempt),
				attribute.String("error.message", err.Error()),
			))
		}
	}

	result.err = err

	if err != nil {
		stepSpan.RecordError(err)
		stepSpan.SetStatus(codes.Error, err.Error())
		stepSpan.AddEvent("step.failed", trace.WithAttributes(
			attribute.String("error.message", err.Error()),
		))
	} else {
		stepSpan.AddEvent("step.completed")
	}

	return result
}

// buildStepLinks creates links to previous steps for dependency tracking.
func (w *Workflow) buildStepLinks(index int, step *stepDef) []trace.Link {
	var links []trace.Link

	w.mu.Lock()
	completed := append([]*stepResult(nil), w.completedSteps...)
	w.mu.Unlock()

	// Link to previous step
	if step.linkToPrevious && index > 0 && index-1 < len(completed) {
		prevResult := completed[index-1]
		if prevResult.spanCtx.IsValid() {
			links = append(links, trace.Link{
				SpanContext: prevResult.spanCtx,
				Attributes: []attribute.KeyValue{
					attribute.String("workflow.link.type", "sequence"),
					attribute.String("workflow.link.from_step", prevResult.def.name),
				},
			})
		}
	}

	// Link to specific steps by name
	for _, stepName := range step.linkTo {
		for _, completed := range completed {
			if completed.def.name == stepName && completed.spanCtx.IsValid() {
				links = append(links, trace.Link{
					SpanContext: completed.spanCtx,
					Attributes: []attribute.KeyValue{
						attribute.String("workflow.link.type", "dependency"),
						attribute.String("workflow.link.from_step", stepName),
					},
				})
				break
			}
		}
	}

	return links
}

// calculateBackoff computes the backoff duration in milliseconds for a retry attempt.
func (w *Workflow) calculateBackoff(config *RetryConfig, attempt int) int64 {
	if config == nil || config.BackoffMs <= 0 {
		return 0
	}

	backoff := config.BackoffMs

	// Apply multiplier for exponential backoff
	if config.Multiplier > 1.0 {
		for i := 1; i < attempt; i++ {
			backoff = int64(float64(backoff) * config.Multiplier)
			if config.MaxBackoff > 0 && backoff >= config.MaxBackoff {
				break // stop early; also guards against overflow on long retry chains
			}
		}
	}

	// Cap at max backoff
	if config.MaxBackoff > 0 && backoff > config.MaxBackoff {
		backoff = config.MaxBackoff
	}

	// Equal jitter: spread retries over [backoff/2, backoff) so that many workflows
	// failing at once do not retry in lockstep.
	if config.Jitter && backoff > 1 {
		half := backoff / 2
		backoff = half + rand.Int64N(backoff-half)
	}

	return backoff
}

// sleepContext waits for d, returning early with the context error if ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// compensate runs compensation handlers in reverse order.
//
// Compensation deliberately runs on a context detached from cancellation: a
// workflow is most likely to need rollback precisely when its context has been
// cancelled or timed out, and a cancelled compensation leaves state half-applied.
// Deadlines for individual compensations are the handler's responsibility.
func (w *Workflow) compensate(ctx context.Context) error {
	ctx = trace.ContextWithSpan(context.WithoutCancel(ctx), w.workflowSpan)

	w.mu.Lock()
	w.state = StateCompensating
	completed := append([]*stepResult(nil), w.completedSteps...)
	w.mu.Unlock()

	w.workflowSpan.SetAttributes(attribute.String("workflow.state", string(StateCompensating)))
	w.workflowSpan.AddEvent("workflow.compensating", trace.WithAttributes(
		attribute.Int("workflow.steps_to_compensate", len(completed)),
	))

	var compensationErrors []error

	// Run compensations in reverse order (skip the failed step)
	for i := len(completed) - 1; i >= 0; i-- {
		result := completed[i]

		// Skip if step wasn't successfully executed or has no compensation
		if result.err != nil || result.def.compensation == nil {
			continue
		}

		err := w.executeCompensation(ctx, i, result)
		if err != nil {
			compensationErrors = append(compensationErrors, err)
		}
	}

	if len(compensationErrors) > 0 {
		w.mu.Lock()
		w.state = StateFailed
		w.mu.Unlock()
		return fmt.Errorf("compensation errors: %v", compensationErrors)
	}

	w.mu.Lock()
	w.state = StateCompensated
	w.mu.Unlock()

	w.workflowSpan.SetAttributes(attribute.String("workflow.state", string(StateCompensated)))
	w.workflowSpan.AddEvent("workflow.compensated")

	return nil
}

// executeCompensation runs a single compensation handler.
func (w *Workflow) executeCompensation(ctx context.Context, index int, result *stepResult) error {
	attrs := []attribute.KeyValue{
		attribute.String("workflow.name", w.name),
		attribute.String("workflow.step.name", result.def.name),
		attribute.Int("workflow.step.index", index),
		attribute.Bool("workflow.compensation", true),
	}

	// Link compensation span to original step span
	link := trace.Link{
		SpanContext: result.spanCtx,
		Attributes: []attribute.KeyValue{
			attribute.String("link.type", "compensation"),
		},
	}

	compCtx, compSpan := w.tracer.Start(ctx, fmt.Sprintf("workflow.compensate.%s", result.def.name),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
		trace.WithLinks(link),
	)
	defer compSpan.End()

	compSpan.AddEvent("compensation.started")

	err := result.def.compensation(compCtx, compSpan)

	if err != nil {
		compSpan.RecordError(err)
		compSpan.SetStatus(codes.Error, err.Error())
		compSpan.AddEvent("compensation.failed", trace.WithAttributes(
			attribute.String("error.message", err.Error()),
		))
		return fmt.Errorf("compensation for step '%s' failed: %w", result.def.name, err)
	}

	compSpan.AddEvent("compensation.completed")
	return nil
}

// CompensateAll manually triggers compensation for all completed steps.
// Use this when CompensationMode is CompensateManual.
func (w *Workflow) CompensateAll(ctx context.Context) error {
	w.mu.Lock()
	if w.state == StateCompensating || w.state == StateCompensated {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	return w.compensate(ctx)
}

// State returns the current workflow state.
func (w *Workflow) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// Context returns the workflow's context (with the workflow span).
func (w *Workflow) Context() context.Context {
	return w.workflowContext
}

// Span returns the workflow-level span for adding custom attributes.
func (w *Workflow) Span() trace.Span {
	return w.workflowSpan
}
