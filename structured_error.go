package autotel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// StructuredError is an error written for the three people who meet it: the
// user who reads Message, the support agent who follows Link, and the
// engineer who reads Why and Fix in a trace.
//
//	return &autotel.StructuredError{
//	    Message: "Daily limit reached",
//	    Why:     "This amount would take today's total past the £300 limit.",
//	    Fix:     "The limit resets at midnight.",
//	    Code:    "DAILY_LIMIT_EXCEEDED",
//	    Status:  429,
//	    Cause:   err,
//	}
//
// A span that records it (autotel.Trace does, for any error its function
// returns) gains error.why, error.fix, error.link, error.code, error.status
// and error.details.* alongside the usual exception event, so a query can
// group failures by what went wrong rather than by message text.
type StructuredError struct {
	Message string
	Why     string
	Fix     string
	Link    string
	Code    string
	// Status is the HTTP-style status a caller branches on. Zero means unset,
	// and ParseError reports it as 500.
	Status  int
	Details map[string]any
	// Internal is backend-only context. It is never written by MarshalJSON,
	// so it cannot reach a client through an error response.
	Internal map[string]any
	Cause    error
}

func (e *StructuredError) Error() string { return e.Message }

func (e *StructuredError) Unwrap() error { return e.Cause }

// Format writes every field with %+v, one per line, the way the error reads
// in a terminal. %v and %s write Message alone.
func (e *StructuredError) Format(f fmt.State, verb rune) {
	if verb != 'v' || !f.Flag('+') {
		fmt.Fprint(f, e.Message)
		return
	}

	lines := []string{e.Message}
	for _, field := range [][2]string{{"Why", e.Why}, {"Fix", e.Fix}, {"Link", e.Link}, {"Code", e.Code}} {
		if field[1] != "" {
			lines = append(lines, "  "+field[0]+": "+field[1])
		}
	}
	if e.Status != 0 {
		lines = append(lines, fmt.Sprintf("  Status: %d", e.Status))
	}
	if e.Cause != nil {
		lines = append(lines, "  Caused by: "+e.Cause.Error())
	}
	fmt.Fprint(f, strings.Join(lines, "\n"))
}

// MarshalJSON writes the client-safe shape of the error. Internal is left out,
// and the cause is reduced to its message.
func (e *StructuredError) MarshalJSON() ([]byte, error) {
	type data struct {
		Why  string `json:"why,omitempty"`
		Fix  string `json:"fix,omitempty"`
		Link string `json:"link,omitempty"`
	}
	type cause struct {
		Message string `json:"message"`
	}
	out := struct {
		Message string         `json:"message"`
		Status  int            `json:"status,omitempty"`
		Data    *data          `json:"data,omitempty"`
		Code    string         `json:"code,omitempty"`
		Details map[string]any `json:"details,omitempty"`
		Cause   *cause         `json:"cause,omitempty"`
	}{Message: e.Message, Status: e.Status, Code: e.Code, Details: e.Details}

	if e.Why != "" || e.Fix != "" || e.Link != "" {
		out.Data = &data{e.Why, e.Fix, e.Link}
	}
	if e.Cause != nil {
		out.Cause = &cause{e.Cause.Error()}
	}

	return json.Marshal(out)
}

// ParsedError is any error read as the fields a caller acts on.
type ParsedError struct {
	Message string
	// Status is 500 unless a StructuredError in the chain set one.
	Status  int
	Why     string
	Fix     string
	Link    string
	Code    string
	Details map[string]any
	Raw     error
}

// ParseError reads err the way a client would: the first StructuredError in
// its chain supplies the fields, and anything else becomes a 500 carrying its
// message. Parse once where the error is caught, and use typed fields from
// there on.
func ParseError(err error) ParsedError {
	var structured *StructuredError
	if !errors.As(err, &structured) {
		message := "An error occurred"
		if err != nil {
			message = err.Error()
		}

		return ParsedError{Message: message, Status: 500, Raw: err}
	}

	status := structured.Status
	if status == 0 {
		status = 500
	}

	return ParsedError{
		Message: structured.Message,
		Status:  status,
		Why:     structured.Why,
		Fix:     structured.Fix,
		Link:    structured.Link,
		Code:    structured.Code,
		Details: structured.Details,
		Raw:     err,
	}
}

// errorAttributes is what a StructuredError in err's chain adds to a span
// that records it. It is empty for any other error.
func errorAttributes(err error) map[string]any {
	var structured *StructuredError
	if !errors.As(err, &structured) {
		return nil
	}

	attrs := map[string]any{
		"error.why":  structured.Why,
		"error.fix":  structured.Fix,
		"error.link": structured.Link,
		"error.code": structured.Code,
	}
	for key, value := range attrs {
		if value == "" {
			delete(attrs, key)
		}
	}
	if structured.Status != 0 {
		attrs["error.status"] = structured.Status
	}
	for key, value := range flatten(structured.Details) {
		attrs["error.details."+key] = value
	}

	return attrs
}
