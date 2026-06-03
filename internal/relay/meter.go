package relay

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// broadcastSession tracks cumulative ingress and egress bytes for a single
// announced broadcast path. It is minted at ANNOUNCE time and lives until
// the publisher session ends.
type broadcastSession struct {
	id           string // UUID v4, minted at ANNOUNCE
	ownerTokenID string // token_id from the credential introspection response

	ingressBytes atomic.Uint64
	egressBytes  atomic.Uint64
}

func newBroadcastSession(ownerTokenID string) *broadcastSession {
	return &broadcastSession{
		id:           newUUIDv4(),
		ownerTokenID: ownerTokenID,
	}
}

func (s *broadcastSession) addIngress(n uint64) { s.ingressBytes.Add(n) }
func (s *broadcastSession) addEgress(n uint64)  { s.egressBytes.Add(n) }

func (s *broadcastSession) toEvent() UsageEvent {
	return UsageEvent{
		BroadcastSessionID: s.id,
		OwnerTokenID:       s.ownerTokenID,
		Metrics: map[string]uint64{
			"gateway.ingress_bytes": s.ingressBytes.Load(),
			"gateway.egress_bytes":  s.egressBytes.Load(),
		},
		Ts: time.Now().UTC().Format(time.RFC3339),
	}
}

// Meter manages active broadcast sessions and periodically reports cumulative
// usage to the backend. A single Meter is shared across all publisher sessions
// on a relay node.
type Meter struct {
	client   *BackendClient
	interval time.Duration

	mu       sync.Mutex
	sessions map[*broadcastSession]struct{}
}

func newMeter(client *BackendClient) *Meter {
	return &Meter{
		client:   client,
		interval: 30 * time.Second,
		sessions: make(map[*broadcastSession]struct{}),
	}
}

// Register adds a broadcast session to the active set so it is included
// in periodic usage reports.
func (m *Meter) Register(sess *broadcastSession) {
	m.mu.Lock()
	m.sessions[sess] = struct{}{}
	m.mu.Unlock()
}

// Deregister removes a session from the active set and sends its final
// cumulative usage report before returning.
func (m *Meter) Deregister(ctx context.Context, sess *broadcastSession) {
	m.mu.Lock()
	delete(m.sessions, sess)
	m.mu.Unlock()

	event := sess.toEvent()
	if err := m.client.ReportUsage(ctx, []UsageEvent{event}); err != nil {
		slog.Warn("meter: final usage report failed",
			"session_id", sess.id,
			"error", err)
	}
}

// Run starts the periodic reporter; it blocks until ctx is cancelled.
// Start it in a goroutine alongside the relay server.
func (m *Meter) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.report(ctx)
		}
	}
}

func (m *Meter) report(ctx context.Context) {
	m.mu.Lock()
	events := make([]UsageEvent, 0, len(m.sessions))
	for sess := range m.sessions {
		events = append(events, sess.toEvent())
	}
	m.mu.Unlock()

	if len(events) == 0 {
		return
	}
	if err := m.client.ReportUsage(ctx, events); err != nil {
		slog.Warn("meter: periodic usage report failed", "error", err)
	}
}

// newUUIDv4 generates a random UUID v4 string using crypto/rand.
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
