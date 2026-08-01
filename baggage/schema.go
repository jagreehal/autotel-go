package baggage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/baggage"
)

// FieldType represents the type of a baggage field.
type FieldType int

const (
	FieldTypeString FieldType = iota
	FieldTypeNumber
	FieldTypeBoolean
	FieldTypeEnum
)

// FieldConstraint defines validation constraints for a field.
//
// MinValue and MaxValue are pointers so that a bound of 0 is enforceable and
// distinguishable from "no bound set". Use Bound to set them.
type FieldConstraint struct {
	Type        FieldType
	Required    bool
	MaxLength   int      // For strings
	MinValue    *float64 // For numbers; nil means no lower bound
	MaxValue    *float64 // For numbers; nil means no upper bound
	EnumValues  []string // For enums
	Pattern     string   // Regex pattern for strings
	HashValue   bool     // Hash the value for PII protection
	RedactPII   bool     // Auto-detect and redact PII
	Description string   // Documentation

	compiledPattern *regexp.Regexp // compiled once at DefineField
}

// Bound returns a pointer to v, for use with FieldConstraint.MinValue/MaxValue.
//
// Example:
//
//	schema.DefineField("discount", baggage.FieldConstraint{
//	    Type:     baggage.FieldTypeNumber,
//	    MinValue: baggage.Bound(0),
//	    MaxValue: baggage.Bound(100),
//	})
func Bound(v float64) *float64 { return &v }

// PIIPattern represents a pattern for detecting PII.
type PIIPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// Common PII patterns
var DefaultPIIPatterns = []PIIPattern{
	{Name: "email", Pattern: regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)},
	{Name: "phone", Pattern: regexp.MustCompile(`(\+?1?[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}`)},
	{Name: "ssn", Pattern: regexp.MustCompile(`\d{3}[-\s]?\d{2}[-\s]?\d{4}`)},
	{Name: "credit_card", Pattern: regexp.MustCompile(`\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}`)},
	{Name: "ip_address", Pattern: regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)},
}

// Schema defines the structure and validation rules for baggage.
type Schema struct {
	fields           map[string]*FieldConstraint
	piiPatterns      []PIIPattern
	maxTotalSize     int
	onError          func(field string, value string, err error)
	strictMode       bool           // Reject unknown fields
	cardinalityLimit map[string]int // Per-field cardinality limits
}

// SchemaOption configures the schema.
type SchemaOption func(*Schema)

// WithMaxTotalSize sets the maximum total size of all baggage values.
func WithMaxTotalSize(size int) SchemaOption {
	return func(s *Schema) {
		s.maxTotalSize = size
	}
}

// WithPIIPatterns sets custom PII patterns.
func WithPIIPatterns(patterns []PIIPattern) SchemaOption {
	return func(s *Schema) {
		s.piiPatterns = patterns
	}
}

// WithOnError sets an error callback.
func WithOnError(fn func(field string, value string, err error)) SchemaOption {
	return func(s *Schema) {
		s.onError = fn
	}
}

// WithStrictMode enables strict mode (rejects unknown fields).
func WithStrictMode(enabled bool) SchemaOption {
	return func(s *Schema) {
		s.strictMode = enabled
	}
}

// WithCardinalityLimit sets a cardinality limit for a field.
func WithCardinalityLimit(field string, limit int) SchemaOption {
	return func(s *Schema) {
		if s.cardinalityLimit == nil {
			s.cardinalityLimit = make(map[string]int)
		}
		s.cardinalityLimit[field] = limit
	}
}

// NewSchema creates a new baggage schema.
func NewSchema(opts ...SchemaOption) *Schema {
	s := &Schema{
		fields:           make(map[string]*FieldConstraint),
		piiPatterns:      DefaultPIIPatterns,
		maxTotalSize:     8192, // 8KB default
		cardinalityLimit: make(map[string]int),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// DefineField adds a field definition to the schema.
//
// Example:
//
//	schema.DefineField("tenant_id", baggage.FieldConstraint{
//	    Type:      baggage.FieldTypeString,
//	    Required:  true,
//	    MaxLength: 64,
//	})
//
//	schema.DefineField("priority", baggage.FieldConstraint{
//	    Type:       baggage.FieldTypeEnum,
//	    EnumValues: []string{"low", "normal", "high", "critical"},
//	})
//
//	schema.DefineField("user_id", baggage.FieldConstraint{
//	    Type:      baggage.FieldTypeString,
//	    HashValue: true, // Hash for privacy
//	})
//
// An invalid Pattern is rejected at definition time rather than silently failing
// on every Validate call.
func (s *Schema) DefineField(name string, constraint FieldConstraint) *Schema {
	if constraint.Pattern != "" {
		constraint.compiledPattern = regexp.MustCompile(constraint.Pattern)
	}
	s.fields[name] = &constraint
	return s
}

// DefineStringField is a convenience method for defining a string field.
func (s *Schema) DefineStringField(name string, maxLength int, required bool) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type:      FieldTypeString,
		MaxLength: maxLength,
		Required:  required,
	})
}

// DefineHashedField is a convenience method for defining a hashed field.
func (s *Schema) DefineHashedField(name string) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type:      FieldTypeString,
		HashValue: true,
	})
}

// DefineEnumField is a convenience method for defining an enum field.
func (s *Schema) DefineEnumField(name string, values []string) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type:       FieldTypeEnum,
		EnumValues: values,
	})
}

// DefineNumberField is a convenience method for defining a number field with
// an inclusive [min, max] range. Both bounds are enforced, including 0.
func (s *Schema) DefineNumberField(name string, min, max float64) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type:     FieldTypeNumber,
		MinValue: Bound(min),
		MaxValue: Bound(max),
	})
}

// DefineBoolField is a convenience method for defining a boolean field.
func (s *Schema) DefineBoolField(name string) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type: FieldTypeBoolean,
	})
}

// DefinePIIField is a convenience method for defining a field with PII redaction.
func (s *Schema) DefinePIIField(name string) *Schema {
	return s.DefineField(name, FieldConstraint{
		Type:      FieldTypeString,
		RedactPII: true,
	})
}

// ValidationError represents a validation error.
type ValidationError struct {
	Field   string
	Value   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("baggage validation error for field '%s': %s", e.Field, e.Message)
}

// Validate validates a value against the schema.
func (s *Schema) Validate(field, value string) error {
	constraint, ok := s.fields[field]
	if !ok {
		if s.strictMode {
			return &ValidationError{Field: field, Value: value, Message: "unknown field"}
		}
		return nil
	}

	if value == "" {
		if constraint.Required {
			return &ValidationError{Field: field, Value: value, Message: "required field is empty"}
		}
		return nil
	}

	switch constraint.Type {
	case FieldTypeString:
		return s.validateString(field, value, constraint)
	case FieldTypeNumber:
		return s.validateNumber(field, value, constraint)
	case FieldTypeBoolean:
		return s.validateBoolean(field, value, constraint)
	case FieldTypeEnum:
		return s.validateEnum(field, value, constraint)
	}

	return nil
}

func (s *Schema) validateString(field, value string, c *FieldConstraint) error {
	if c.MaxLength > 0 && len(value) > c.MaxLength {
		return &ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("value exceeds max length of %d", c.MaxLength),
		}
	}

	if c.compiledPattern != nil && !c.compiledPattern.MatchString(value) {
		return &ValidationError{Field: field, Value: value, Message: "value does not match pattern"}
	}

	return nil
}

func (s *Schema) validateNumber(field, value string, c *FieldConstraint) error {
	num, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return &ValidationError{Field: field, Value: value, Message: "not a valid number"}
	}

	if c.MinValue != nil && num < *c.MinValue {
		return &ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("value below minimum of %g", *c.MinValue),
		}
	}

	if c.MaxValue != nil && num > *c.MaxValue {
		return &ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("value above maximum of %g", *c.MaxValue),
		}
	}

	return nil
}

func (s *Schema) validateBoolean(field, value string, c *FieldConstraint) error {
	lower := strings.ToLower(value)
	if lower != "true" && lower != "false" && lower != "1" && lower != "0" {
		return &ValidationError{Field: field, Value: value, Message: "not a valid boolean"}
	}
	return nil
}

func (s *Schema) validateEnum(field, value string, c *FieldConstraint) error {
	for _, v := range c.EnumValues {
		if v == value {
			return nil
		}
	}
	return &ValidationError{
		Field:   field,
		Value:   value,
		Message: fmt.Sprintf("value not in allowed enum values: %v", c.EnumValues),
	}
}

// Process validates and transforms a value according to the schema.
// Returns the processed value and any error.
func (s *Schema) Process(field, value string) (string, error) {
	if err := s.Validate(field, value); err != nil {
		if s.onError != nil {
			s.onError(field, value, err)
		}
		return "", err
	}

	constraint, ok := s.fields[field]
	if !ok {
		// Unknown field, return as-is if not strict mode
		return value, nil
	}

	// Apply transformations
	if constraint.RedactPII {
		value = s.redactPII(value)
	}

	if constraint.HashValue {
		value = hashString(value)
	}

	// Truncate if needed
	if constraint.MaxLength > 0 && len(value) > constraint.MaxLength {
		value = value[:constraint.MaxLength]
	}

	return value, nil
}

// redactPII replaces PII patterns with placeholders.
func (s *Schema) redactPII(value string) string {
	for _, pattern := range s.piiPatterns {
		value = pattern.Pattern.ReplaceAllString(value, "[REDACTED:"+pattern.Name+"]")
	}
	return value
}

// hashString creates a short hash of the value.
func hashString(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])[:16]
}

// DetectPII checks if a value contains PII.
func (s *Schema) DetectPII(value string) []string {
	var detected []string
	for _, pattern := range s.piiPatterns {
		if pattern.Pattern.MatchString(value) {
			detected = append(detected, pattern.Name)
		}
	}
	return detected
}

// SafeBaggage wraps baggage operations with schema validation.
//
// A SafeBaggage is safe for concurrent use and is designed to be shared across
// requests: cardinality tracking is deliberately process-wide, since its purpose
// is to catch a field whose value space grows without bound.
type SafeBaggage struct {
	schema *Schema

	mu         sync.Mutex
	seenValues map[string]map[string]bool // Per-field value tracking for cardinality
}

// NewSafeBaggage creates a new safe baggage wrapper.
func NewSafeBaggage(schema *Schema) *SafeBaggage {
	return &SafeBaggage{
		schema:     schema,
		seenValues: make(map[string]map[string]bool),
	}
}

// Set validates and sets a baggage value.
func (sb *SafeBaggage) Set(ctx context.Context, field, value string) (context.Context, error) {
	processed, err := sb.schema.Process(field, value)
	if err != nil {
		return ctx, err
	}

	if processed == "" {
		return ctx, nil
	}

	// Check cardinality
	if limit, ok := sb.schema.cardinalityLimit[field]; ok {
		if err := sb.trackCardinality(field, processed, limit); err != nil {
			return ctx, err
		}
	}

	// Check total size against the baggage actually carried by this context.
	// Size is a property of the context, not of this SafeBaggage instance.
	if size := baggageSize(ctx) + len(field) + len(processed) + 2; size > sb.schema.maxTotalSize {
		return ctx, &ValidationError{
			Field:   field,
			Value:   processed,
			Message: fmt.Sprintf("total baggage size would exceed limit of %d bytes", sb.schema.maxTotalSize),
		}
	}

	member, err := baggage.NewMember(field, processed)
	if err != nil {
		return ctx, err
	}

	bag := baggage.FromContext(ctx)
	bag, err = bag.SetMember(member)
	if err != nil {
		return ctx, err
	}

	return baggage.ContextWithBaggage(ctx, bag), nil
}

// trackCardinality records a distinct value for a field and reports when the
// field's value space exceeds its configured limit.
func (sb *SafeBaggage) trackCardinality(field, value string, limit int) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.seenValues[field] == nil {
		sb.seenValues[field] = make(map[string]bool)
	}
	if !sb.seenValues[field][value] && len(sb.seenValues[field]) >= limit {
		return &ValidationError{
			Field:   field,
			Value:   value,
			Message: fmt.Sprintf("cardinality limit of %d exceeded", limit),
		}
	}
	sb.seenValues[field][value] = true

	return nil
}

// baggageSize returns the encoded size of the baggage carried by ctx.
func baggageSize(ctx context.Context) int {
	size := 0
	for _, member := range baggage.FromContext(ctx).Members() {
		size += len(member.Key()) + len(member.Value()) + 2 // +2 for = and separator
	}
	return size
}

// SetMultiple validates and sets multiple baggage values.
func (sb *SafeBaggage) SetMultiple(ctx context.Context, values map[string]string) (context.Context, error) {
	var err error
	for field, value := range values {
		ctx, err = sb.Set(ctx, field, value)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// Get retrieves a baggage value.
func (sb *SafeBaggage) Get(ctx context.Context, field string) string {
	bag := baggage.FromContext(ctx)
	return bag.Member(field).Value()
}

// GetAll retrieves all baggage values defined in the schema.
func (sb *SafeBaggage) GetAll(ctx context.Context) map[string]string {
	result := make(map[string]string)
	bag := baggage.FromContext(ctx)

	for _, member := range bag.Members() {
		if _, ok := sb.schema.fields[member.Key()]; ok || !sb.schema.strictMode {
			result[member.Key()] = member.Value()
		}
	}

	return result
}

// CheckRequiredFields verifies all required fields are present.
func (sb *SafeBaggage) CheckRequiredFields(ctx context.Context) []string {
	var missing []string
	bag := baggage.FromContext(ctx)

	for field, constraint := range sb.schema.fields {
		if constraint.Required {
			if bag.Member(field).Value() == "" {
				missing = append(missing, field)
			}
		}
	}

	return missing
}

// DefaultSchema returns a schema with common business context fields.
func DefaultSchema() *Schema {
	return NewSchema().
		DefineStringField(KeyTenantID, 64, false).
		DefineHashedField(KeyUserID).
		DefineStringField(KeyOrderID, 64, false).
		DefineStringField(KeyWorkflowID, 64, false).
		DefineStringField(KeyRequestID, 64, false).
		DefineStringField(KeyRegion, 32, false).
		DefineStringField(KeyChannel, 32, false).
		DefineEnumField(KeyEnvironment, []string{"development", "staging", "production"}).
		DefineStringField(KeyVersion, 32, false).
		DefineStringField(KeyExperiment, 64, false).
		DefineEnumField(KeyPriority, []string{"low", "normal", "high", "critical"})
}
