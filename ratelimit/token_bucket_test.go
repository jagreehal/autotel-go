package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTokenBucket_Allow(t *testing.T) {
	tb := NewTokenBucket(10, 10) // 10 spans/sec, burst of 10

	// Should allow first 10 spans
	for i := 0; i < 10; i++ {
		assert.True(t, tb.Allow(), "should allow span %d", i)
	}

	// Should block 11th span immediately
	assert.False(t, tb.Allow(), "should block 11th span")

	// Wait and try again
	time.Sleep(100 * time.Millisecond)
	assert.True(t, tb.Allow(), "should have refilled ~1 token")
}

func TestTokenBucket_RateLimit(t *testing.T) {
	tb := NewTokenBucket(5, 5) // 5 spans/sec

	// Consume all tokens
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow())
	}

	// Should be blocked
	assert.False(t, tb.Allow())

	// Wait 1 second - should refill 5 tokens
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow(), "should allow after refill")
	}
}

func TestTokenBucket_Burst(t *testing.T) {
	tb := NewTokenBucket(1, 10) // 1 span/sec, burst of 10

	// Should allow burst of 10 immediately
	for i := 0; i < 10; i++ {
		assert.True(t, tb.Allow(), "should allow burst span %d", i)
	}

	// Should block after burst
	assert.False(t, tb.Allow())
}
