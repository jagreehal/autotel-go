// Package baggage provides safe business context propagation helpers.
// It wraps OpenTelemetry baggage with guardrails to prevent PII leakage
// and high-cardinality explosions.
package baggage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.opentelemetry.io/otel/baggage"
)

// Standard business context keys
const (
	KeyTenantID    = "tenant_id"
	KeyUserID      = "user_id"
	KeyOrderID     = "order_id"
	KeyWorkflowID  = "workflow_id"
	KeyRequestID   = "request_id"
	KeyRegion      = "region"
	KeyChannel     = "channel"
	KeyEnvironment = "environment"
	KeyVersion     = "version"
	KeyExperiment  = "experiment"
	KeyPriority    = "priority"
)

// Profile defines which baggage keys are allowed and how they should be processed.
type Profile struct {
	// AllowedKeys lists keys that can be set. If empty, all keys are allowed.
	AllowedKeys map[string]bool

	// HashKeys lists keys whose values should be hashed (for PII protection).
	HashKeys map[string]bool

	// MaxValueLength limits value length (truncates if exceeded).
	MaxValueLength int

	// AllowedValues restricts certain keys to specific values (low cardinality).
	AllowedValues map[string][]string
}

// DefaultProfile returns a safe default profile for business context.
// It allows common business keys and hashes potentially sensitive ones.
func DefaultProfile() *Profile {
	return &Profile{
		AllowedKeys: map[string]bool{
			KeyTenantID:    true,
			KeyUserID:      true,
			KeyOrderID:     true,
			KeyWorkflowID:  true,
			KeyRequestID:   true,
			KeyRegion:      true,
			KeyChannel:     true,
			KeyEnvironment: true,
			KeyVersion:     true,
			KeyExperiment:  true,
			KeyPriority:    true,
		},
		HashKeys: map[string]bool{
			KeyUserID: true, // Hash user IDs by default for privacy
		},
		MaxValueLength: 128,
		AllowedValues: map[string][]string{
			KeyPriority:    {"low", "normal", "high", "critical"},
			KeyEnvironment: {"development", "staging", "production"},
		},
	}
}

// StrictProfile returns a more restrictive profile for high-security contexts.
func StrictProfile() *Profile {
	return &Profile{
		AllowedKeys: map[string]bool{
			KeyTenantID:    true,
			KeyRegion:      true,
			KeyEnvironment: true,
			KeyWorkflowID:  true,
		},
		HashKeys: map[string]bool{
			KeyTenantID:   true,
			KeyWorkflowID: true,
		},
		MaxValueLength: 64,
	}
}

// BusinessContext manages business context in baggage with safety guardrails.
type BusinessContext struct {
	profile *Profile
}

// New creates a new BusinessContext with the default profile.
func New() *BusinessContext {
	return &BusinessContext{profile: DefaultProfile()}
}

// NewWithProfile creates a BusinessContext with a custom profile.
func NewWithProfile(profile *Profile) *BusinessContext {
	return &BusinessContext{profile: profile}
}

// Set adds a business context value to the context's baggage.
// Values are validated and potentially hashed according to the profile.
func (bc *BusinessContext) Set(ctx context.Context, key, value string) (context.Context, error) {
	processedValue := bc.processValue(key, value)
	if processedValue == "" {
		return ctx, nil // Silently skip disallowed or empty values
	}

	member, err := baggage.NewMember(key, processedValue)
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

// SetMultiple adds multiple business context values at once.
func (bc *BusinessContext) SetMultiple(ctx context.Context, values map[string]string) (context.Context, error) {
	var err error
	for key, value := range values {
		ctx, err = bc.Set(ctx, key, value)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// Get retrieves a business context value from baggage.
// Note: Hashed values cannot be reversed.
func (bc *BusinessContext) Get(ctx context.Context, key string) string {
	bag := baggage.FromContext(ctx)
	return bag.Member(key).Value()
}

// GetAll retrieves all business context values from baggage.
func (bc *BusinessContext) GetAll(ctx context.Context) map[string]string {
	result := make(map[string]string)
	bag := baggage.FromContext(ctx)

	for _, member := range bag.Members() {
		if bc.profile.AllowedKeys == nil || bc.profile.AllowedKeys[member.Key()] {
			result[member.Key()] = member.Value()
		}
	}

	return result
}

// processValue validates and transforms a value according to the profile.
func (bc *BusinessContext) processValue(key, value string) string {
	// Check if key is allowed
	if bc.profile.AllowedKeys != nil && !bc.profile.AllowedKeys[key] {
		return ""
	}

	// Check allowed values for restricted keys
	if allowed, ok := bc.profile.AllowedValues[key]; ok {
		valid := false
		for _, v := range allowed {
			if v == value {
				valid = true
				break
			}
		}
		if !valid {
			return ""
		}
	}

	// Truncate if too long
	if bc.profile.MaxValueLength > 0 && len(value) > bc.profile.MaxValueLength {
		value = value[:bc.profile.MaxValueLength]
	}

	// Hash if required
	if bc.profile.HashKeys[key] {
		value = hashValue(value)
	}

	return value
}

// hashValue creates a short hash of the value (first 16 chars of SHA256).
func hashValue(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])[:16]
}

// Convenience functions for common operations

// WithTenant adds tenant_id to the context.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	ctx, _ = New().Set(ctx, KeyTenantID, tenantID)
	return ctx
}

// WithUser adds a hashed user_id to the context.
func WithUser(ctx context.Context, userID string) context.Context {
	ctx, _ = New().Set(ctx, KeyUserID, userID)
	return ctx
}

// WithOrder adds order_id to the context.
func WithOrder(ctx context.Context, orderID string) context.Context {
	ctx, _ = New().Set(ctx, KeyOrderID, orderID)
	return ctx
}

// WithWorkflow adds workflow_id to the context.
func WithWorkflow(ctx context.Context, workflowID string) context.Context {
	ctx, _ = New().Set(ctx, KeyWorkflowID, workflowID)
	return ctx
}

// WithRegion adds region to the context.
func WithRegion(ctx context.Context, region string) context.Context {
	ctx, _ = New().Set(ctx, KeyRegion, region)
	return ctx
}

// WithBusinessContext adds multiple business context values at once.
//
// Example:
//
//	ctx = baggage.WithBusinessContext(ctx,
//	    baggage.KeyTenantID, "acme-corp",
//	    baggage.KeyOrderID, "order-123",
//	    baggage.KeyRegion, "us-west-2",
//	)
func WithBusinessContext(ctx context.Context, kvPairs ...string) context.Context {
	if len(kvPairs)%2 != 0 {
		return ctx // Invalid pairs, return unchanged
	}

	bc := New()
	for i := 0; i < len(kvPairs); i += 2 {
		ctx, _ = bc.Set(ctx, kvPairs[i], kvPairs[i+1])
	}
	return ctx
}

// ExtractToAttributes extracts baggage values as span attributes.
// Useful for adding business context to spans.
func ExtractToAttributes(ctx context.Context) map[string]string {
	return New().GetAll(ctx)
}

// ProfileBuilder helps construct custom profiles.
type ProfileBuilder struct {
	profile *Profile
}

// NewProfileBuilder creates a new profile builder starting from defaults.
func NewProfileBuilder() *ProfileBuilder {
	return &ProfileBuilder{
		profile: &Profile{
			AllowedKeys:    make(map[string]bool),
			HashKeys:       make(map[string]bool),
			MaxValueLength: 128,
			AllowedValues:  make(map[string][]string),
		},
	}
}

// AllowKey adds a key to the allowed list.
func (b *ProfileBuilder) AllowKey(key string) *ProfileBuilder {
	b.profile.AllowedKeys[key] = true
	return b
}

// AllowKeys adds multiple keys to the allowed list.
func (b *ProfileBuilder) AllowKeys(keys ...string) *ProfileBuilder {
	for _, key := range keys {
		b.profile.AllowedKeys[key] = true
	}
	return b
}

// HashKey marks a key for hashing.
func (b *ProfileBuilder) HashKey(key string) *ProfileBuilder {
	b.profile.HashKeys[key] = true
	return b
}

// RestrictValues limits a key to specific values.
func (b *ProfileBuilder) RestrictValues(key string, values ...string) *ProfileBuilder {
	b.profile.AllowedValues[key] = values
	return b
}

// MaxLength sets the maximum value length.
func (b *ProfileBuilder) MaxLength(length int) *ProfileBuilder {
	b.profile.MaxValueLength = length
	return b
}

// Build returns the constructed profile.
func (b *ProfileBuilder) Build() *Profile {
	return b.profile
}

// SanitizeForLogging removes or masks sensitive baggage values for safe logging.
func SanitizeForLogging(ctx context.Context) map[string]string {
	result := make(map[string]string)
	bag := baggage.FromContext(ctx)

	sensitiveKeys := map[string]bool{
		KeyUserID: true,
	}

	for _, member := range bag.Members() {
		key := member.Key()
		value := member.Value()

		if sensitiveKeys[key] {
			// Mask all but last 4 chars
			if len(value) > 4 {
				value = strings.Repeat("*", len(value)-4) + value[len(value)-4:]
			} else {
				value = "****"
			}
		}

		result[key] = value
	}

	return result
}
