package redaction

import (
	"regexp"
	"strings"
)

// PIIRedactor detects and redacts PII from attribute values.
type PIIRedactor struct {
	patterns      map[string]*regexp.Regexp
	allowlistKeys map[string]bool
	enabled       bool
}

// PIIRedactorOption configures a PII redactor.
type PIIRedactorOption func(*PIIRedactor)

// WithCustomPattern adds a custom PII pattern.
func WithCustomPattern(name string, pattern *regexp.Regexp) PIIRedactorOption {
	return func(r *PIIRedactor) {
		r.patterns[name] = pattern
	}
}

// WithAllowlistKeys sets keys that should never be redacted.
func WithAllowlistKeys(keys ...string) PIIRedactorOption {
	return func(r *PIIRedactor) {
		for _, key := range keys {
			r.allowlistKeys[key] = true
		}
	}
}

// WithEnabled controls whether redaction is enabled.
func WithEnabled(enabled bool) PIIRedactorOption {
	return func(r *PIIRedactor) {
		r.enabled = enabled
	}
}

// NewPIIRedactor creates a new PII redactor.
func NewPIIRedactor(opts ...PIIRedactorOption) *PIIRedactor {
	r := &PIIRedactor{
		patterns: map[string]*regexp.Regexp{
			"email":       regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
			"phone":       regexp.MustCompile(`\b\d{3}[-.\s]?\d{3}[-.\s]?\d{4}\b`),
			"ssn":         regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			"credit_card": regexp.MustCompile(`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`),
		},
		allowlistKeys: make(map[string]bool),
		enabled:       true,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Redact redacts PII from a value if the key is not allowlisted.
func (r *PIIRedactor) Redact(key string, value string) string {
	if !r.enabled || r.allowlistKeys[key] {
		return value
	}

	redacted := value
	for name, pattern := range r.patterns {
		replacement := "[" + strings.ToUpper(name) + "_REDACTED]"
		redacted = pattern.ReplaceAllString(redacted, replacement)
	}

	return redacted
}
