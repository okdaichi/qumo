package relay

import (
	"context"
	"time"
)

// openTimeout bounds each OpenGroupAt call with defaultGroupTimeout while
// amortizing the context and timer across a subscriber's lifetime, instead of
// constructing a context.WithTimeout (one context + one runtime timer + 4
// allocations) per delivered group. The fast path — open succeeds before the
// timer fires, the overwhelmingly common case since a healthy open takes tens
// of µs against a 30ms budget — is a timer Reset+Stop with zero allocation.
// Only when the timer actually fires (MAX_STREAMS backpressure) is the
// cancelled context unusable and rebuilt.
//
// Reuse is safe because gomoqt's OpenGroupAt does not retain the context after
// returning: it is consumed only by quic-go's OpenUniStreamSync while blocking
// for stream credit, so cancelling it after a successful open has no effect on
// the opened group.
//
// Not safe for concurrent use; owned by a single egress goroutine.
type openTimeout struct {
	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
}

// newOpenTimeout builds an openTimeout whose contexts derive from parent
// (the subscriber's track-writer context, so subscriber cancellation still
// propagates into a blocked open). Call release when the egress loop exits.
func newOpenTimeout(parent context.Context) *openTimeout {
	o := &openTimeout{parent: parent}
	o.arm()
	return o
}

// arm builds a fresh context and its cancel timer (stopped). The timer's
// callback captures the cancel of the context built alongside it, so a late
// fire from a previous generation can only cancel its own, already-discarded
// context.
func (o *openTimeout) arm() {
	o.ctx, o.cancel = context.WithCancel(o.parent)
	o.timer = time.AfterFunc(defaultGroupTimeout, o.cancel)
	o.timer.Stop()
}

// start arms the deadline and returns the context to pass to OpenGroupAt.
// Every start must be paired with exactly one done immediately after the
// guarded call returns.
func (o *openTimeout) start() context.Context {
	o.timer.Reset(defaultGroupTimeout)
	return o.ctx
}

// done disarms the deadline and reports whether it fired. On fire the shared
// context is cancelled and cannot be reused, so a fresh one is built (rare
// path: only under MAX_STREAMS backpressure or a stalled peer).
func (o *openTimeout) done() (timedOut bool) {
	if o.timer.Stop() {
		return false
	}
	o.arm()
	return true
}

// release stops the timer and cancels the current context, detaching it from
// the parent's children list. Idempotent.
func (o *openTimeout) release() {
	o.timer.Stop()
	o.cancel()
}
