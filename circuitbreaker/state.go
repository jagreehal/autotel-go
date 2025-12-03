package circuitbreaker

// State represents circuit breaker state
type State int

const (
	// StateClosed means the circuit is closed and operations proceed normally
	StateClosed State = iota
	// StateOpen means the circuit is open and operations are blocked
	StateOpen
	// StateHalfOpen means the circuit is testing recovery
	StateHalfOpen
)

// String returns the string representation of the state
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
