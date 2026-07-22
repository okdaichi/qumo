package relay

import (
	"context"
	"math/rand/v2"
	"time"
)

// DialBackoff implements exponential backoff with jitter for outbound
// connection-establishment retries. It transforms burst arrivals into a
// gradual ramp, helping the relay survive simultaneous handshake load
// spikes without dropping connections.
//
// DialBackoff is not safe for concurrent use; each goroutine that needs
// backoff must have its own instance.
//
// Use cases:
//   - Peer relay reconnection (maintainPeer)
//   - Client-side dial retry (loadgen)
//
// Zero value: Base=1s, Max=30s, MaxAttempts=0 (unlimited).
type DialBackoff struct {
	Base        time.Duration // starting interval
	Max         time.Duration // ceiling
	MaxAttempts int           // 0 = unlimited; after this many, Wait returns false
	attempt     int
}

// Wait blocks until either the backoff delay elapses or ctx is cancelled.
// It returns false if ctx was cancelled (caller should stop) or if
// maxAttempts has been reached. Each call advances the backoff state.
//
// The delay is computed as:
//
//	delay = min(base * 2^attempt, max)
//	delay *= uniform(0.75, 1.25)   // ±25 % jitter
func (b *DialBackoff) Wait(ctx context.Context) bool {
	if b.MaxAttempts > 0 && b.attempt >= b.MaxAttempts {
		return false
	}

	// Exponential factor, capped to avoid overflow on large attempt counts.
	// When attempt >= 10, maintain the max exponent (2⁹ = 512× base) rather
	// than falling back to 1× base; the max cap below enforces the actual
	// ceiling so overflow is never exposed.
	var exp int
	if b.attempt < 10 {
		exp = 1 << b.attempt
	} else {
		exp = 1 << 9
	}
	delay := b.Base * time.Duration(exp)

	// Apply ceiling.
	if b.Max > 0 && delay > b.Max {
		delay = b.Max
	}

	// Apply ±25 % jitter.
	jitter := time.Duration(float64(delay) * (0.75 + 0.5*rand.Float64()))

	b.attempt++

	t := time.NewTimer(jitter)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Attempts returns the current retry attempt count (0 = no retries yet).
// Useful for logging in callers that want to expose retry progress.
func (b *DialBackoff) Attempts() int {
	return b.attempt
}

// Reset restores the backoff to its initial state. Call this after a
// successful connection is established so that a subsequent disconnect
// reconnection starts from the minimum delay.
func (b *DialBackoff) Reset() {
	b.attempt = 0
}

// jitterDelay waits for a random duration in [0, max] or until ctx is
// cancelled. It returns false if ctx was cancelled. Used to spread out
// synchronized reconnect attempts after a batch of simultaneous peer
// disconnects, reducing the thundering-herd effect on the peer's
// handshake capacity.
func jitterDelay(ctx context.Context, max time.Duration) bool {
	delay := time.Duration(rand.Float64() * float64(max))
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
