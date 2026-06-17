package relay

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

// authTrackName is the well-known MoQ track name on which publishers send
// their JWT credential. The relay subscribes to this track at ANNOUNCE time.
const authTrackName moqt.TrackName = "auth"

type Server struct {
	// MOQServer is the underlying MoQT server. The caller is responsible for
	// setting Addr, TLSConfig (must include all accepted ALPNs, e.g. ["h3", "moqt"]),
	// QUICConfig, and WebTransportServer. Handler and TrackMux are wired by init().
	MOQServer *moqt.Server
	// MOQDialer is used for outbound peer connections. The caller must set
	// TLSConfig with NextProtos: []string{moqt.NextProtoMOQ} only, so that
	// ALPN negotiation does not accidentally select "h3".
	MOQDialer *moqt.Dialer
	Config    *Config
	TrackMux  *moqt.TrackMux

	// credentialClient is non-nil when QUMO_CREDENTIAL_URL is configured.
	// It handles credential introspection and usage reporting.
	credentialClient *CredentialClient
	// meter drives periodic and final usage reporting for metered sessions.
	// It is non-nil exactly when credentialClient is non-nil.
	meter *Meter

	webtransportHandler *moqt.WebTransportHandler
	statusHandler       *statusHandler
	initOnce            sync.Once

	// resolvers for peer discovery
	localResolver  PeerResolver // Nomad native (within-cluster)
	remoteResolver PeerResolver // Remote traffic resolver (cross-cluster)

	// connectedMu guards connected, which tracks peer addresses already dialing
	// or connected to prevent duplicate maintainPeer goroutines.
	connectedMu sync.Mutex
	connected   map[string]struct{}
}

func (s *Server) ServeHealth(w http.ResponseWriter, r *http.Request) {
	s.init()
	if s.statusHandler == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	s.statusHandler.ServeHTTP(w, r)
}

func (s *Server) HandleWebTransport(w http.ResponseWriter, r *http.Request) {
	s.init()
	if s.webtransportHandler == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	s.webtransportHandler.ServeHTTP(w, r)
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		if s.TrackMux == nil {
			s.TrackMux = moqt.NewTrackMux(0)
		}

		if s.statusHandler == nil {
			s.statusHandler = newStatusHandler()
		}

		// Wire relay-specific fields into the caller-provided MoQServer.
		if s.MOQServer.Handler != nil {
			slog.Warn("relay.Server: overriding MOQServer.Handler set by caller")
		}
		// Native QUIC connections are always relay peers (ALPN "moqt").
		// WebTransport connections (ALPN "h3") are publisher/browser sessions
		// that require credential auth when the credential client is configured.
		s.MOQServer.Handler = moqt.HandleFunc(s.relayPeer)
		if s.MOQServer.TrackMux != nil {
			slog.Warn("relay.Server: overriding MOQServer.TrackMux set by caller")
		}
		s.MOQServer.TrackMux = s.TrackMux

		s.webtransportHandler = &moqt.WebTransportHandler{
			TrackMux: s.TrackMux,
			Handler:  moqt.HandleFunc(s.Relay),
			Logger:   s.MOQServer.Logger,
		}

		// ConnContext intercepts each accepted QUIC connection before the MOQ
		// handshake. For native QUIC connections the underlying type satisfies
		// connStatsProvider, so we launch a polling goroutine to collect
		// connection-level stats (RTT, packet loss). WebTransport connections
		// do not satisfy the interface and are silently skipped.
		s.MOQServer.ConnContext = func(ctx context.Context, conn moqt.StreamConn) context.Context {
			if provider, ok := conn.(connStatsProvider); ok {
				addr := conn.RemoteAddr().String()
				go pollConnStats(conn.Context(), provider, addr)
			}
			return ctx
		}

		// Invariant: meter must be set whenever credentialClient is set.
		// A manually-constructed Server that sets credentialClient without meter
		// would panic later when the first metered announcement is accepted.
		if s.credentialClient != nil && s.meter == nil {
			panic("relay.Server: meter must be non-nil when credentialClient is set")
		}

		if s.connected == nil {
			s.connected = make(map[string]struct{})
		}
	})
}

// ListenAndServe starts the relay server.
func (s *Server) ListenAndServe() error {
	if s.MOQServer == nil {
		panic("relay.Server: MoQServer is required")
	}
	if s.MOQDialer == nil {
		panic("relay.Server: MoQDialer is required")
	}

	s.init()

	// Start server - this will block until server closes
	return s.MOQServer.ListenAndServe()
}

func (s *Server) Close() error {
	if s.MOQServer != nil {
		_ = s.MOQServer.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.MOQServer != nil {
		return s.MOQServer.Shutdown(ctx)
	}

	return nil
}

// ConnectPeers dials configured peer relays and discovers their announcements
// via ANNOUNCE_PLEASE. Received announcements are registered on the local
// TrackMux so that subscribers can transparently access remote content.
// It also starts peer discovery loops for configured resolvers.
// It blocks until ctx is cancelled.
func (s *Server) ConnectPeers(ctx context.Context) {
	s.init()
	var wg sync.WaitGroup

	// Static peers from config.
	for _, peer := range s.Config.Peers {
		if !s.markConnected(peer.Address) {
			continue
		}
		wg.Go(func() {
			s.maintainPeer(ctx, peer)
			s.markUnconnected(peer.Address)
		})
	}

	// Dynamic peer discovery loops.
	// Local resolver: local cluster discovery (edge→all hubs, default→flat peers).
	if s.localResolver != nil && s.Config.LocalResolverInterval > 0 {
		wg.Go(func() {
			s.discoverPeers(ctx, &wg, s.Config.LocalResolverInterval, s.localResolver)
		})
	}

	// Remote resolver: cross-cluster hub discovery.
	if s.remoteResolver != nil && s.Config.RemoteResolverInterval > 0 {
		wg.Go(func() {
			s.discoverPeers(ctx, &wg, s.Config.RemoteResolverInterval, s.remoteResolver)
		})
	}

	wg.Wait()
}

// filterPeersByAddr removes peers whose addresses are present in the exclude map.
func filterPeersByAddr(peers []ResolvedPeer, exclude map[string]struct{}) []ResolvedPeer {
	filtered := make([]ResolvedPeer, 0, len(peers))
	for _, p := range peers {
		if _, ok := exclude[p.Address]; ok {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

// discoverPeers runs the role-aware peer discovery loop using resolver.
// It builds topology connections according to the node's role (edge/hub/default)
// and re-checks at interval. Already-connected peers are skipped.
func (s *Server) discoverPeers(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, resolver PeerResolver) {
	// connect dials each peer not already connected (server-wide dedup by address).
	connect := func(peers []ResolvedPeer) {
		for _, p := range peers {
			if !s.markConnected(p.Address) {
				continue
			}
			p := p
			wg.Go(func() {
				s.maintainPeer(ctx, Peer{Address: p.Address})
				s.markUnconnected(p.Address)
			})
		}
	}

	// connectFirst dials only the first peer from the slice.
	connectFirst := func(peers []ResolvedPeer) {
		if len(peers) > 0 {
			connect(peers[:1])
		}
	}

	tick := func() {
		switch s.Config.Role {
		case "edge":
			// Edge nodes connect to ALL local hubs (load-balanced content relay).
			// No edge-to-edge or cross-region connections.
			// Only use the local (Nomad) resolver — never query the enterprise resolver.
			if resolver == s.localResolver {
				if peers, err := resolver.ResolvePeers(ctx, PeerQuery{Role: "hub"}); err == nil {
					connect(peers)
				}
			}

		case "hub":
			// Hub nodes connect to remote hubs (cross-cluster) via remote resolver.
			// No local hub-to-hub connections (reduces hops/latency within cluster).
			if resolver == s.remoteResolver {
				if peers, err := resolver.ResolvePeers(ctx, PeerQuery{Role: "hub"}); err == nil {
					connectFirst(peers)
				}
			}
			// When local resolver (Nomad) is the resolver being used, hubs
			// do nothing — no local hub connections needed.

		default:
			// Flat discovery: any peers in the cluster (for nodes without role set).
			// Only use the local (Nomad) resolver.
			if resolver == s.localResolver {
				if peers, err := resolver.ResolvePeers(ctx, PeerQuery{Limit: 5}); err == nil {
					connect(peers)
				}
			}
		}
	}

	tick()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// markConnected records addr as connected and returns true if it was not already present.
// It is safe for concurrent use.
func (s *Server) markConnected(addr string) bool {
	s.connectedMu.Lock()
	defer s.connectedMu.Unlock()
	if _, ok := s.connected[addr]; ok {
		return false
	}
	s.connected[addr] = struct{}{}
	metricPeersConnected.Inc()
	return true
}

// markUnconnected removes addr from the connected set.
// It is safe for concurrent use.
func (s *Server) markUnconnected(addr string) {
	s.connectedMu.Lock()
	defer s.connectedMu.Unlock()
	delete(s.connected, addr)
	metricPeersConnected.Dec()
	metricSessionRTTMilliseconds.DeleteLabelValues(addr)
	metricSessionEstimatedBitrate.DeleteLabelValues(addr)
}

func (s *Server) maintainPeer(ctx context.Context, peer Peer) {
	for {
		if ctx.Err() != nil {
			return
		}

		sess, err := s.MOQDialer.DialQUIC(ctx, peer.Address, s.TrackMux)
		if err != nil {
			metricPeerDialAttempts.WithLabelValues(peer.Address, "error").Inc()
			slog.Warn("failed to dial peer", "address", peer.Address, "error", err)
			if !waitRetry(ctx, 5*time.Second) {
				return
			}
			continue
		}
		metricPeerDialAttempts.WithLabelValues(peer.Address, "ok").Inc()

		s.relayPeer(sess)

		<-sess.Context().Done()

		slog.Info("peer disconnected", "address", peer.Address)

		if !waitRetry(ctx, 5*time.Second) {
			return
		}
	}
}

// waitRetry waits for the specified duration or until ctx is cancelled.
// Returns false if ctx was cancelled.
func waitRetry(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Relay handles inbound WebTransport sessions (publishers and browser clients).
// When a backend client is configured, each announced broadcast path is
// authenticated via a JWT read from the "auth" MoQ track before being accepted.
func (s *Server) Relay(sess *moqt.Session) {
	s.serveSession(sess, true)
}

// relayPeer handles native QUIC sessions from trusted relay peers.
// These sessions are authenticated at the transport layer (mTLS) and bypass
// the per-announcement JWT credential check.
func (s *Server) relayPeer(sess *moqt.Session) {
	s.serveSession(sess, false)
}

// serveSession is the shared core for Relay and relayPeer.
// requireAuth=true enables per-announcement JWT authentication (publisher path).
func (s *Server) serveSession(sess *moqt.Session, requireAuth bool) {
	s.init()
	defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())

	metricSessionsActive.Inc()
	defer metricSessionsActive.Dec()

	addr := sess.RemoteAddr().String()
	go pollSessionStats(sess, addr)

	slog.Info("relay: new session", "remote", addr, "peer", !requireAuth)

	announced, err := sess.AcceptAnnounce("/")
	if err != nil {
		slog.Warn("failed to accept announcement", "error", err)
		return
	}
	for {
		ann, err := announced.ReceiveAnnouncement(sess.Context())
		if err != nil {
			slog.Warn("relay: announcements loop ended",
				"remote", sess.RemoteAddr(),
				"error", err,
				"reader_ctx_err", announced.Context().Err(),
				"sess_ctx_err", sess.Context().Err())
			return
		}

		// Authenticate publisher announcements when the credential client is configured.
		var broadSess *broadcastSession
		if requireAuth && s.credentialClient != nil {
			broadSess, err = s.authenticateAnnouncement(sess.Context(), sess, ann)
			if err != nil {
				// MoQ has no per-announcement error response, so the publisher
				// receives no explicit rejection — the ANNOUNCE is simply not
				// mirrored into the TrackMux.
				slog.Warn("relay: announcement rejected: credential check failed",
					"broadcast_path", ann.BroadcastPath(),
					"error", err)
				continue
			}
		}

		handler := newRelayHandler(ann, sess, s.Config.NodeID, broadSess)

		// Route selection: only replace an existing active handler if the new
		// route is strictly better. This is evaluated once per new candidate to
		// preserve group-cache hit rates and playback continuity.
		if _, existing := s.TrackMux.TrackHandler(ann.BroadcastPath()); existing != nil {
			if rr, ok := existing.(RouteReporter); ok {
				better, reason := isBetterRoute(handler.RouteStats(), rr.RouteStats())
				if !better {
					metricRouteRejections.WithLabelValues(string(reason)).Inc()
					handler.cancel()
					continue
				}
				metricRouteReplacements.Inc()
				// Gracefully drain the displaced handler.
				if dr, ok := existing.(Drainable); ok {
					dr.Drain(DrainTimeout)
				}
			}
		}

		// Track the broadcast route and release it when the handler's context
		// is cancelled (covers both normal session end and drain expiry).
		metricBroadcastsActive.Inc()
		context.AfterFunc(handler.ctx, func() { metricBroadcastsActive.Dec() })

		// Ensure handler.cancel is called when the session ends to release
		// the child context from the parent's internal children list.
		context.AfterFunc(sess.Context(), handler.cancel)

		// Register the broadcast session with the meter so usage is reported
		// periodically and on session close.
		if broadSess != nil {
			s.meter.Register(broadSess)
			context.AfterFunc(handler.ctx, func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				s.meter.Deregister(shutdownCtx, broadSess)
			})
		}

		s.TrackMux.Announce(ann, handler)
	}
}

// authenticateAnnouncement subscribes to the "auth" track on the announced
// broadcast path, reads the JWT from the first frame, and introspects it
// against the credential introspection endpoint.
//
// Publisher-side contract: the publisher must serve a single-group track named
// "auth" on the announced broadcast path. The group must contain at least one
// frame whose payload is the raw JWT bytes (no framing). The relay expects the
// complete JWT to arrive within the 5-second authCtx deadline.
//
// Returns the minted broadcastSession on success, or an error if authentication
// fails (missing track, empty JWT, or invalid/expired credential).
func (s *Server) authenticateAnnouncement(ctx context.Context, sess *moqt.Session, ann *moqt.Announcement) (*broadcastSession, error) {
	authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	reader, err := sess.Subscribe(authCtx, ann.BroadcastPath(), authTrackName, nil)
	if err != nil {
		return nil, fmt.Errorf("subscribe auth track: %w", err)
	}

	gr, err := reader.AcceptGroup(authCtx)
	if err != nil {
		return nil, fmt.Errorf("accept auth group: %w", err)
	}

	buf := DefaultFramePool.Get()
	defer DefaultFramePool.Put(buf)

	var jwtBuf bytes.Buffer
	for frame := range gr.Frames(buf) {
		jwtBuf.Write(frame.Body())
	}
	if jwtBuf.Len() == 0 {
		return nil, fmt.Errorf("auth track: empty JWT")
	}
	jwt := jwtBuf.String()

	result, err := s.credentialClient.Introspect(authCtx, jwt)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("credential rejected by backend")
	}

	return newBroadcastSession(result.TokenID), nil
}
