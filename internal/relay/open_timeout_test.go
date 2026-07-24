package relay

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenTimeout_Done_FastPath(t *testing.T) {
	o := newOpenTimeout(context.Background())
	defer o.release()

	ctx := o.start()
	require.NoError(t, ctx.Err())
	timedOut := o.done()

	assert.False(t, timedOut)
	assert.NoError(t, ctx.Err(), "context must stay usable when the deadline did not fire")

	// The context is reused across deliveries on the fast path.
	assert.Same(t, ctx, o.start())
	assert.False(t, o.done())
}

func TestOpenTimeout_Done_TimerFired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := newOpenTimeout(context.Background())
		defer o.release()

		ctx := o.start()
		// Let the deadline fire (fake time inside the synctest bubble).
		time.Sleep(defaultGroupTimeout + time.Millisecond)
		synctest.Wait() // ensure the AfterFunc cancel has run

		assert.Error(t, ctx.Err(), "deadline fire must cancel the in-flight context")
		timedOut := o.done()
		assert.True(t, timedOut)

		// After a fire the context is rebuilt: the next delivery gets a live one.
		next := o.start()
		assert.NoError(t, next.Err())
		assert.False(t, o.done())
	})
}

func TestOpenTimeout_Start_ParentCancelled(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	o := newOpenTimeout(parent)
	defer o.release()

	ctx := o.start()
	cancel()
	assert.Error(t, ctx.Err(), "parent (subscriber) cancellation must reach a blocked open")
	// Not a timeout: the deadline never fired.
	assert.False(t, o.done())
}

func TestOpenTimeout_Release(t *testing.T) {
	o := newOpenTimeout(context.Background())
	ctx := o.start()
	o.release()
	assert.Error(t, ctx.Err(), "release must cancel the current context")
	o.release() // idempotent
}

// BenchmarkOpenDeadline compares the former per-delivery context.WithTimeout
// against the reusable openTimeout fast path (the shape deliverGroup executes
// per delivered group).
func BenchmarkOpenDeadline(b *testing.B) {
	b.Run("WithTimeout", func(b *testing.B) {
		parent := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			ctx, cancel := context.WithTimeout(parent, defaultGroupTimeout)
			_ = ctx.Err() // not actionable: sink so the deadline check stays in the measured loop
			cancel()
		}
	})
	b.Run("Reusable", func(b *testing.B) {
		o := newOpenTimeout(context.Background())
		defer o.release()
		b.ReportAllocs()
		for b.Loop() {
			ctx := o.start()
			_ = ctx.Err() // not actionable: sink so the deadline check stays in the measured loop
			_ = o.done()
		}
	})
}
