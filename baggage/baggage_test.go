package baggage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBusinessContext_Set(t *testing.T) {
	bc := New()
	ctx := context.Background()

	ctx, err := bc.Set(ctx, KeyTenantID, "acme-corp")
	require.NoError(t, err)

	value := bc.Get(ctx, KeyTenantID)
	assert.Equal(t, "acme-corp", value)
}

func TestBusinessContext_SetMultiple(t *testing.T) {
	bc := New()
	ctx := context.Background()

	ctx, err := bc.SetMultiple(ctx, map[string]string{
		KeyTenantID: "acme-corp",
		KeyRegion:   "us-west-2",
		KeyOrderID:  "order-123",
	})
	require.NoError(t, err)

	assert.Equal(t, "acme-corp", bc.Get(ctx, KeyTenantID))
	assert.Equal(t, "us-west-2", bc.Get(ctx, KeyRegion))
	assert.Equal(t, "order-123", bc.Get(ctx, KeyOrderID))
}

func TestBusinessContext_HashSensitiveValues(t *testing.T) {
	bc := New()
	ctx := context.Background()

	// UserID should be hashed by default profile
	ctx, err := bc.Set(ctx, KeyUserID, "user-12345")
	require.NoError(t, err)

	value := bc.Get(ctx, KeyUserID)
	// Should not be the original value
	assert.NotEqual(t, "user-12345", value)
	// Should be a hash (16 chars)
	assert.Len(t, value, 16)
}

func TestBusinessContext_DisallowedKey(t *testing.T) {
	bc := New()
	ctx := context.Background()

	// "secret_key" is not in allowed keys
	ctx, err := bc.Set(ctx, "secret_key", "password123")
	require.NoError(t, err)

	// Should not be set
	value := bc.Get(ctx, "secret_key")
	assert.Empty(t, value)
}

func TestBusinessContext_RestrictedValues(t *testing.T) {
	bc := New()
	ctx := context.Background()

	// Priority is restricted to specific values
	ctx, err := bc.Set(ctx, KeyPriority, "high")
	require.NoError(t, err)
	assert.Equal(t, "high", bc.Get(ctx, KeyPriority))

	// Invalid priority value should not be set
	ctx, err = bc.Set(ctx, KeyPriority, "super-urgent")
	require.NoError(t, err)
	// Still has old value
	assert.Equal(t, "high", bc.Get(ctx, KeyPriority))
}

func TestBusinessContext_MaxValueLength(t *testing.T) {
	profile := &Profile{
		AllowedKeys:    map[string]bool{KeyOrderID: true},
		MaxValueLength: 10,
	}
	bc := NewWithProfile(profile)
	ctx := context.Background()

	longValue := "this-is-a-very-long-order-id-that-should-be-truncated"
	ctx, err := bc.Set(ctx, KeyOrderID, longValue)
	require.NoError(t, err)

	value := bc.Get(ctx, KeyOrderID)
	assert.Len(t, value, 10)
	assert.Equal(t, "this-is-a-", value)
}

func TestBusinessContext_GetAll(t *testing.T) {
	bc := New()
	ctx := context.Background()

	ctx, _ = bc.SetMultiple(ctx, map[string]string{
		KeyTenantID: "acme",
		KeyRegion:   "eu-west-1",
	})

	all := bc.GetAll(ctx)
	assert.Contains(t, all, KeyTenantID)
	assert.Contains(t, all, KeyRegion)
}

func TestWithBusinessContext(t *testing.T) {
	ctx := context.Background()

	ctx = WithBusinessContext(ctx,
		KeyTenantID, "acme-corp",
		KeyOrderID, "order-456",
		KeyRegion, "us-east-1",
	)

	bc := New()
	assert.Equal(t, "acme-corp", bc.Get(ctx, KeyTenantID))
	assert.Equal(t, "order-456", bc.Get(ctx, KeyOrderID))
	assert.Equal(t, "us-east-1", bc.Get(ctx, KeyRegion))
}

func TestWithBusinessContext_OddPairs(t *testing.T) {
	ctx := context.Background()

	// Odd number of pairs should return unchanged context
	ctx = WithBusinessContext(ctx, KeyTenantID, "acme", "orphan_key")

	bc := New()
	// Nothing should be set
	assert.Empty(t, bc.Get(ctx, KeyTenantID))
}

func TestConvenienceFunctions(t *testing.T) {
	ctx := context.Background()

	ctx = WithTenant(ctx, "tenant-1")
	ctx = WithRegion(ctx, "ap-south-1")
	ctx = WithOrder(ctx, "order-789")
	ctx = WithWorkflow(ctx, "workflow-abc")

	bc := New()
	assert.Equal(t, "tenant-1", bc.Get(ctx, KeyTenantID))
	assert.Equal(t, "ap-south-1", bc.Get(ctx, KeyRegion))
	assert.Equal(t, "order-789", bc.Get(ctx, KeyOrderID))
	assert.Equal(t, "workflow-abc", bc.Get(ctx, KeyWorkflowID))

	// User should be hashed
	ctx = WithUser(ctx, "user-secret")
	userValue := bc.Get(ctx, KeyUserID)
	assert.NotEqual(t, "user-secret", userValue)
	assert.Len(t, userValue, 16)
}

func TestStrictProfile(t *testing.T) {
	bc := NewWithProfile(StrictProfile())
	ctx := context.Background()

	// Allowed in strict profile
	ctx, _ = bc.Set(ctx, KeyTenantID, "acme")
	assert.NotEmpty(t, bc.Get(ctx, KeyTenantID))

	// Not allowed in strict profile
	ctx, _ = bc.Set(ctx, KeyOrderID, "order-123")
	assert.Empty(t, bc.Get(ctx, KeyOrderID))
}

func TestProfileBuilder(t *testing.T) {
	profile := NewProfileBuilder().
		AllowKeys(KeyTenantID, KeyRegion, "custom_key").
		HashKey("custom_key").
		RestrictValues(KeyRegion, "us-east-1", "us-west-2").
		MaxLength(64).
		Build()

	bc := NewWithProfile(profile)
	ctx := context.Background()

	// Allowed key
	ctx, _ = bc.Set(ctx, KeyTenantID, "acme")
	assert.Equal(t, "acme", bc.Get(ctx, KeyTenantID))

	// Custom key (hashed)
	ctx, _ = bc.Set(ctx, "custom_key", "secret-value")
	customValue := bc.Get(ctx, "custom_key")
	assert.NotEqual(t, "secret-value", customValue)
	assert.Len(t, customValue, 16)

	// Restricted values
	ctx, _ = bc.Set(ctx, KeyRegion, "us-east-1")
	assert.Equal(t, "us-east-1", bc.Get(ctx, KeyRegion))

	// Invalid restricted value
	ctx, _ = bc.Set(ctx, KeyRegion, "eu-west-1")
	assert.Equal(t, "us-east-1", bc.Get(ctx, KeyRegion)) // Still old value
}

func TestSanitizeForLogging(t *testing.T) {
	bc := New()
	ctx := context.Background()

	ctx, _ = bc.SetMultiple(ctx, map[string]string{
		KeyTenantID: "acme-corp",
	})

	// Add user (which is hashed in baggage, but the hashed value is sensitive too)
	ctx = WithUser(ctx, "user-12345")

	sanitized := SanitizeForLogging(ctx)

	// Tenant should be visible
	assert.Equal(t, "acme-corp", sanitized[KeyTenantID])

	// User should be masked
	if userVal, ok := sanitized[KeyUserID]; ok {
		// If hashed value is 16 chars, mask shows last 4
		if len(userVal) >= 4 {
			assert.Contains(t, userVal, "****")
		}
	}
}

func TestExtractToAttributes(t *testing.T) {
	ctx := context.Background()

	ctx = WithBusinessContext(ctx,
		KeyTenantID, "acme",
		KeyRegion, "us-west-2",
	)

	attrs := ExtractToAttributes(ctx)
	assert.Equal(t, "acme", attrs[KeyTenantID])
	assert.Equal(t, "us-west-2", attrs[KeyRegion])
}

func TestHashValue_Deterministic(t *testing.T) {
	// Same input should produce same hash
	hash1 := hashValue("test-value")
	hash2 := hashValue("test-value")
	assert.Equal(t, hash1, hash2)

	// Different input should produce different hash
	hash3 := hashValue("different-value")
	assert.NotEqual(t, hash1, hash3)
}
