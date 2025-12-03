package redaction

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPIIRedactor_Email(t *testing.T) {
	r := NewPIIRedactor()
	redacted := r.Redact("email", "user@example.com")
	assert.Equal(t, "[EMAIL_REDACTED]", redacted)
}

func TestPIIRedactor_Phone(t *testing.T) {
	r := NewPIIRedactor()
	redacted := r.Redact("phone", "Call me at 555-123-4567")
	assert.Contains(t, redacted, "[PHONE_REDACTED]")
}

func TestPIIRedactor_SSN(t *testing.T) {
	r := NewPIIRedactor()
	redacted := r.Redact("ssn", "SSN: 123-45-6789")
	assert.Contains(t, redacted, "[SSN_REDACTED]")
}

func TestPIIRedactor_CreditCard(t *testing.T) {
	r := NewPIIRedactor()
	redacted := r.Redact("card", "Card: 1234-5678-9012-3456")
	assert.Contains(t, redacted, "[CREDIT_CARD_REDACTED]")
}

func TestPIIRedactor_Allowlist(t *testing.T) {
	r := NewPIIRedactor(
		WithAllowlistKeys("user_id", "email"),
	)
	redacted := r.Redact("user_id", "user@example.com")
	assert.Equal(t, "user@example.com", redacted) // Not redacted

	redacted = r.Redact("email", "user@example.com")
	assert.Equal(t, "user@example.com", redacted) // Not redacted

	// Other keys should still be redacted
	redacted = r.Redact("other", "user@example.com")
	assert.Equal(t, "[EMAIL_REDACTED]", redacted)
}

func TestPIIRedactor_Disabled(t *testing.T) {
	r := NewPIIRedactor(WithEnabled(false))
	redacted := r.Redact("email", "user@example.com")
	assert.Equal(t, "user@example.com", redacted) // Not redacted
}

func TestPIIRedactor_CustomPattern(t *testing.T) {
	r := NewPIIRedactor(
		WithCustomPattern("api_key", regexp.MustCompile(`\b[A-Za-z0-9]{20,}\b`)),
	)
	redacted := r.Redact("key", "API key: abc123xyz789def456uvw012")
	assert.Contains(t, redacted, "[API_KEY_REDACTED]")
}
