package bank

// Code is a machine-readable refusal, as it appears in telemetry.
type Code string

// The ways the bank says no. Part 7 is about what happens at the boundary: the
// same codes, turned into something a customer, a call-centre agent and an
// on-call engineer can each act on without reading this file.
const (
	CardNotRecognised  Code = "CARD_NOT_RECOGNISED"
	PinIncorrect       Code = "PIN_INCORRECT"
	CardRetained       Code = "CARD_RETAINED"
	InsufficientFunds  Code = "INSUFFICIENT_FUNDS"
	DailyLimitExceeded Code = "DAILY_LIMIT_EXCEEDED"
	LedgerUnavailable  Code = "LEDGER_UNAVAILABLE"
)

// Error is a refusal from the domain.
type Error struct {
	Code    Code
	Message string
	// Detail holds facts about the refusal that are safe to record: attempts
	// left, limits.
	Detail map[string]any
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
