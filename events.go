package autotel

// FunnelStatus represents the status of a funnel step.
type FunnelStatus string

const (
	FunnelStarted   FunnelStatus = "started"
	FunnelCompleted FunnelStatus = "completed"
	FunnelAbandoned FunnelStatus = "abandoned"
	FunnelFailed    FunnelStatus = "failed"
)

// OutcomeStatus represents the outcome of an operation.
type OutcomeStatus string

const (
	OutcomeSuccess OutcomeStatus = "success"
	OutcomeFailure OutcomeStatus = "failure"
	OutcomePartial OutcomeStatus = "partial"
)

// Event represents a trackable event for batch operations.
type Event struct {
	Name       string
	Properties map[string]any
}
