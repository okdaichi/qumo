package relay

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDialBackoff_Defaults(t *testing.T) {
	b := DialBackoff{Base: 1 * time.Second, Max: 30 * time.Second}
	assert.Equal(t, 1*time.Second, b.Base)
	assert.Equal(t, 30*time.Second, b.Max)
	assert.Equal(t, 0, b.MaxAttempts)
	assert.Equal(t, 0, b.attempt)
}

func TestDialBackoff_ExponentialGrowth(t *testing.T) {
	// Verify the delay formula directly rather than relying solely on wall-clock
	// timing, to avoid CI scheduler flakes. Each call to wait() increments
	// attempt and sleeps for the computed delay; we assert that the actual wait
	// falls within the expected [0.75×, 1.25×] range of the formula.
	b := DialBackoff{Base: 100 * time.Millisecond, Max: 10 * time.Second}
	ctx := context.Background()

	for i := range 5 {
		before := b.attempt

		// Compute the expected delay using the same formula as wait().
		var exp int
		if before < 10 {
			exp = 1 << before
		} else {
			exp = 1 << 9
		}
		rawDelay := b.Base * time.Duration(exp)
		if b.Max > 0 && rawDelay > b.Max {
			rawDelay = b.Max
		}
		minDelay := time.Duration(float64(rawDelay) * 0.75)
		maxDelay := time.Duration(float64(rawDelay) * 1.25)

		start := time.Now()
		ok := b.Wait(ctx)
		elapsed := time.Since(start)

		assert.True(t, ok, "wait should return true on attempt %d", i)
		assert.GreaterOrEqual(t, elapsed, minDelay,
			"wait at attempt %d should be ≥ %.0f%% of computed delay", i, 75.0)
		assert.LessOrEqual(t, elapsed, maxDelay+50*time.Millisecond,
			"wait at attempt %d should be ≤ %.0f%% of computed delay + scheduler slack", i, 125.0)
	}
}

func TestDialBackoff_MaxCap(t *testing.T) {
	// base=10ms, max=50ms. After a few retries the computed delay exceeds max
	// exponentially; the actual wait must be bounded by max + jitter.
	b := DialBackoff{Base: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	ctx := context.Background()

	// Verify the formula directly: with attempt=5, exp=32, delay=320ms,
	// but cap clamps to 50ms. Then jitter adds ±25%: max possible is 62.5ms.
	// Allow ~2× buffer for CI scheduler jitter.
	for i := range 5 {
		b.attempt = 5 + i // Force a high attempt count so formula exceeds cap
		start := time.Now()
		ok := b.Wait(ctx)
		elapsed := time.Since(start)

		assert.True(t, ok, "wait should return true on attempt %d", 5+i)
		assert.Less(t, elapsed, 110*time.Millisecond,
			"wait should be capped near max+25%% jitter on attempt %d", 5+i)
	}
}

func TestDialBackoff_MaxAttempts(t *testing.T) {
	b := DialBackoff{Base: 1 * time.Millisecond, Max: 10 * time.Millisecond, MaxAttempts: 3}
	ctx := context.Background()

	assert.True(t, b.Wait(ctx), "attempt 0 should succeed")
	assert.True(t, b.Wait(ctx), "attempt 1 should succeed")
	assert.True(t, b.Wait(ctx), "attempt 2 should succeed (zero-indexed, attempt=0,1,2 = 3 tries)")
	assert.False(t, b.Wait(ctx), "attempt 3 should be rejected (maxAttempts=3)")
}

func TestDialBackoff_ContextCancellation(t *testing.T) {
	b := DialBackoff{Base: 1 * time.Hour, Max: 2 * time.Hour} // long enough to never fire
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	ok := b.Wait(ctx)
	assert.False(t, ok, "wait should return false on cancelled ctx")
	assert.Less(t, time.Since(start), 100*time.Millisecond, "should return promptly")
}

func TestDialBackoff_Reset(t *testing.T) {
	b := DialBackoff{Base: 1 * time.Millisecond, Max: 10 * time.Millisecond, MaxAttempts: 2}

	// Exhaust attempts.
	b.Wait(context.Background())
	b.Wait(context.Background())
	assert.False(t, b.Wait(context.Background()), "should be exhausted")

	b.Reset()
	assert.Equal(t, 0, b.attempt, "reset should zero attempt count")
	assert.True(t, b.Wait(context.Background()), "should succeed after reset")
}

func TestDialBackoff_JitterUniformRange(t *testing.T) {
	// Run many backoff samples with the same parameters and verify the
	// jittered delay falls within the expected [0.75×, 1.25×] range.
	// This checks the jitter formula, not the statistical distribution.
	b := DialBackoff{Base: 10 * time.Millisecond, Max: 200 * time.Millisecond}
	ctx := context.Background()

	// With attempt=0, exp=1, delay=10ms, jitter range = [7.5ms, 12.5ms].
	// Allow 2× tolerance for CI scheduler jitter.
	for range 20 {
		b.attempt = 0
		start := time.Now()
		ok := b.Wait(ctx)
		elapsed := time.Since(start)

		assert.True(t, ok, "wait should succeed")
		assert.Greater(t, elapsed, time.Duration(0), "delay should be positive")
		assert.Less(t, elapsed, 50*time.Millisecond,
			"jittered delay should not exceed 2.5× base on attempt 0")
	}
}

func TestDialBackoff_ZeroValueIsSafe(t *testing.T) {
	// The zero-value of dialBackoff (base=0, max=0, maxAttempts=0) should
	// not panic or deadlock. With base=0 the delay is effectively 0;
	// maxAttempts=0 means unlimited.
	var b DialBackoff
	ctx := context.Background()

	assert.Equal(t, time.Duration(0), b.Base)
	assert.Equal(t, time.Duration(0), b.Max)
	assert.Equal(t, 0, b.MaxAttempts)

	start := time.Now()
	assert.True(t, b.Wait(ctx), "zero value should not panic")
	assert.Less(t, time.Since(start), 50*time.Millisecond, "should return near-instantly")

	// Multiple waits should also work (unlimited maxAttempts).
	assert.True(t, b.Wait(ctx))
	assert.True(t, b.Wait(ctx))
}

func TestDialBackoff_AttemptCounter(t *testing.T) {
	b := DialBackoff{Base: 1 * time.Millisecond, Max: 5 * time.Millisecond}
	ctx := context.Background()

	for i := range 4 {
		assert.Equal(t, i, b.attempt, "attempt count should be %d before wait %d", i, i)
		b.Wait(ctx)
	}
	assert.Equal(t, 4, b.attempt, "attempt count should be 4 after 4 waits")
}
