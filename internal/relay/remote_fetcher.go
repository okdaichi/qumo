package relay

import (
	"context"
	"crypto/tls"
	"log/slog"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/gomoqt/quic"
	"github.com/okdaichi/qumo/internal/sdn"
)

// RemoteFetcher discovers remote broadcast paths via the SDN controller
// and pre-registers handlers on the local TrackMux so that subscribers
// can transparently receive content from other relays.
//
// It periodically polls the SDN announce table and, for each broadcast
// path that is not locally available, dials the appropriate relay and
// subscribes.
type RemoteFetcher struct {
	// SDNClient is used to query the SDN controller for announcements and routes.
	SDNClient *sdn.Client

	// TrackMux is the local mux where remote handlers are registered.
	TrackMux *moqt.TrackMux

	// TLSConfig is the TLS configuration for outgoing relay-to-relay QUIC connections.
	TLSConfig *tls.Config

	// QUICConfig is the QUIC configuration for outgoing relay-to-relay connections.
	QUICConfig *quic.Config

	// PollInterval is how often to query the SDN for new announcements.
	// Default: 5s.
	PollInterval time.Duration

	// GroupCacheSize for relay handlers created for remote tracks.
	GroupCacheSize int

	// FramePool shared across remote relay handlers.
	FramePool *FramePool

	mu       sync.Mutex
	sessions map[string]*remoteSession     // address → session
	tracked  map[string]context.CancelFunc // broadcastPath → cancel func
	client   *moqt.Client
}

// remoteSession holds a connection to a remote relay.
type remoteSession struct {
	session  *moqt.Session
	refCount int
}

// Run starts the periodic poll loop. It blocks until ctx is cancelled.
func (f *RemoteFetcher) Run(ctx context.Context) {
	f.mu.Lock()
	f.sessions = make(map[string]*remoteSession)
	f.tracked = make(map[string]context.CancelFunc)
	f.client = &moqt.Client{
		TLSConfig:  f.TLSConfig,
		QUICConfig: f.QUICConfig,
	}
	f.mu.Unlock()

	interval := f.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	gcSize := f.GroupCacheSize
	if gcSize <= 0 {
		gcSize = DefaultGroupCacheSize
	}

	pool := f.FramePool
	if pool == nil {
		pool = DefaultFramePool
	}

	slog.Info("remote fetcher started", "poll_interval", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			f.cleanup()
			return
		case <-ticker.C:
			f.poll(ctx, gcSize, pool)
		}
	}
}

// poll queries the SDN for all announcements and registers handlers for
// any broadcast paths not yet locally available.
func (f *RemoteFetcher) poll(ctx context.Context, gcSize int, pool *FramePool) {
	entries, err := f.SDNClient.ListAll(ctx)
	if err != nil {
		slog.Warn("remote fetcher: failed to list announcements", "error", err)
		return
	}

	// Build set of currently announced remote broadcast paths
	remoteSet := make(map[string]string) // broadcastPath → relay name
	for _, e := range entries {
		// Keep the first relay found for each broadcast path
		if _, exists := remoteSet[e.BroadcastPath]; !exists {
			remoteSet[e.BroadcastPath] = e.Relay
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Register new remote paths
	for bp, relay := range remoteSet {
		if _, already := f.tracked[bp]; already {
			continue // already tracking
		}

		// Check if locally available — TrackHandler returns a nil Announcement
		// when no handler is registered for the path.
		ann, _ := f.TrackMux.TrackHandler(moqt.BroadcastPath(bp))
		if ann != nil {
			continue // local handler exists
		}

		// Need to fetch remotely
		f.startRemoteHandler(ctx, bp, relay, gcSize, pool)
	}

	// Remove tracked paths that are no longer in the remote set
	for bp, cancel := range f.tracked {
		if _, exists := remoteSet[bp]; !exists {
			cancel()
			delete(f.tracked, bp)
		}
	}
}

// startRemoteHandler dials the source relay (via SDN routing) and registers
// a relay handler on the local mux. Caller must hold f.mu.
func (f *RemoteFetcher) startRemoteHandler(ctx context.Context, broadcastPath, sourceRelay string, gcSize int, pool *FramePool) {
	// Query SDN for route to source relay
	route, err := f.SDNClient.Route(ctx, sourceRelay)
	if err != nil {
		slog.Warn("remote fetcher: route query failed",
			"broadcast_path", broadcastPath,
			"target", sourceRelay,
			"error", err)
		return
	}

	nextHopAddr := route.NextHopAddress
	if nextHopAddr == "" {
		slog.Warn("remote fetcher: next hop has no address",
			"broadcast_path", broadcastPath,
			"next_hop", route.NextHop)
		return
	}

	// Get or create session to next hop
	rs, err := f.getOrDialSession(ctx, nextHopAddr)
	if err != nil {
		slog.Warn("remote fetcher: failed to dial next hop",
			"address", nextHopAddr,
			"error", err)
		return
	}

	// Create a child context that we can cancel when this path is removed
	pathCtx, cancel := context.WithCancel(ctx)
	f.tracked[broadcastPath] = cancel
	rs.refCount++

	// Register a handler on the local mux via Publish.
	// The handler subscribes to the remote relay on demand (when a subscriber
	// requests a track name under this broadcast path).
	handler := &RelayHandler{
		Session:        rs.session,
		GroupCacheSize: gcSize,
		FramePool:      pool,
		relaying:       make(map[moqt.TrackName]*trackDistributor),
	}

	// Publish registers a virtual announcement + handler.
	// It stays active until pathCtx is cancelled.
	f.TrackMux.Publish(pathCtx, moqt.BroadcastPath(broadcastPath), handler)

	slog.Info("remote fetcher: registered remote handler",
		"broadcast_path", broadcastPath,
		"source_relay", sourceRelay,
		"next_hop", route.NextHop,
		"next_hop_addr", nextHopAddr)

	// Monitor the path context for cleanup
	go func() {
		<-pathCtx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()
		if rs, ok := f.sessions[nextHopAddr]; ok {
			rs.refCount--
			if rs.refCount <= 0 {
				rs.session.CloseWithError(moqt.NoError, "no more remote tracks")
				delete(f.sessions, nextHopAddr)
			}
		}
	}()
}

// getOrDialSession returns an existing session or dials a new one.
// Caller must hold f.mu.
func (f *RemoteFetcher) getOrDialSession(ctx context.Context, address string) (*remoteSession, error) {
	if rs, ok := f.sessions[address]; ok {
		// Check if session is still alive
		if rs.session.Context().Err() == nil {
			return rs, nil
		}
		// Session is dead, remove and reconnect
		delete(f.sessions, address)
	}

	// Dial new connection — release lock during dial
	f.mu.Unlock()
	sess, err := f.client.Dial(ctx, address, f.TrackMux)
	f.mu.Lock()

	if err != nil {
		return nil, err
	}

	// Double-check: another goroutine might have created the session
	if rs, ok := f.sessions[address]; ok {
		// Use the existing session, close our new one
		sess.CloseWithError(moqt.NoError, "duplicate session")
		return rs, nil
	}

	rs := &remoteSession{
		session: sess,
	}
	f.sessions[address] = rs
	return rs, nil
}

// cleanup closes all remote sessions. Called when the fetcher is stopping.
func (f *RemoteFetcher) cleanup() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for bp, cancel := range f.tracked {
		cancel()
		delete(f.tracked, bp)
	}

	for addr, rs := range f.sessions {
		rs.session.CloseWithError(moqt.NoError, "fetcher stopping")
		delete(f.sessions, addr)
	}

	if f.client != nil {
		f.client.Close()
	}

	slog.Info("remote fetcher stopped")
}
