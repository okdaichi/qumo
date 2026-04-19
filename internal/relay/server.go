package relay

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/bootstrap"
)

type Server struct {
	// MoQServer is the underlying MoQT server. The caller is responsible for
	// setting Addr, TLSConfig (must include all accepted ALPNs, e.g. ["h3", "moqt"]),
	// QUICConfig, and WebTransportServer. Handler and TrackMux are wired by init().
	MoQServer *moqt.Server
	// MoQDialer is used for outbound peer connections. The caller must set
	// TLSConfig with NextProtos: []string{moqt.NextProtoMOQ} only, so that
	// ALPN negotiation does not accidentally select "h3".
	MoQDialer *moqt.Dialer
	Config    *Config
	TrackMux  *moqt.TrackMux

	webtransportHandler *moqt.WebTransportHandler
	statusHandler       *statusHandler
	initOnce            sync.Once
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
		s.MoQServer.Handler = moqt.HandleFunc(s.Relay)
		s.MoQServer.TrackMux = s.TrackMux

		s.webtransportHandler = &moqt.WebTransportHandler{
			TrackMux: s.TrackMux,
			Handler:  moqt.HandleFunc(s.Relay),
			Logger:   s.MoQServer.Logger,
		}
	})
}

// ListenAndServe starts the relay server.
func (s *Server) ListenAndServe() error {
	if s.MoQServer == nil {
		panic("relay.Server: MoQServer is required")
	}
	if s.MoQDialer == nil {
		panic("relay.Server: MoQDialer is required")
	}

	s.init()

	// Start server - this will block until server closes
	return s.MoQServer.ListenAndServe()
}

func (s *Server) Close() error {
	if s.MoQServer != nil {
		_ = s.MoQServer.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.MoQServer != nil {
		return s.MoQServer.Shutdown(ctx)
	}

	return nil
}

// ConnectPeers dials configured peer relays and discovers their announcements
// via ANNOUNCE_PLEASE. Received announcements are registered on the local
// TrackMux so that subscribers can transparently access remote content.
// It also starts bootstrap clients for each configured bootstrap server.
// It blocks until ctx is cancelled.
func (s *Server) ConnectPeers(ctx context.Context) {
	s.init()
	var wg sync.WaitGroup

	// Static peers from config.
	for _, peer := range s.Config.Peers {
		wg.Go(func() {
			s.maintainPeer(ctx, peer)
		})
	}

	// Dynamic peers from bootstrap servers.
	for _, bsCfg := range s.Config.Bootstraps {
		bsCfg := bsCfg
		wg.Go(func() {
			client := bootstrap.NewClient(bsCfg, s.Config.NodeID, s.Config.AdvertiseAddr, s.Config.Region, s.Config.Role)
			// Heartbeat goroutine.
			wg.Go(func() {
				client.Run(ctx)
			})
			// Topology-aware peer discovery goroutine.
			s.discoverPeers(ctx, &wg, bsCfg.Interval, client)
		})
	}

	wg.Wait()
}

// discoverPeers runs the role-aware peer discovery loop for a single bootstrap client.
// It builds topology connections according to the node's role (edge/hub/default)
// and re-checks at interval. Already-connected peers are skipped.
func (s *Server) discoverPeers(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, client *bootstrap.Client) {
	var mu sync.Mutex
	connected := make(map[string]struct{})

	// connect dials each peer not already in connected.
	connect := func(peers []bootstrap.Node) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range peers {
			if _, ok := connected[p.ID]; ok {
				continue
			}
			connected[p.ID] = struct{}{}
			p := p
			wg.Go(func() {
				s.maintainPeer(ctx, Peer{Address: p.Addr})
			})
		}
	}

	// connectFirst dials only the first peer from the slice.
	connectFirst := func(peers []bootstrap.Node) {
		if len(peers) > 0 {
			connect(peers[:1])
		}
	}

	tick := func() {
		region := s.Config.Region
		switch s.Config.Role {
		case "edge":
			// 2 local edges + 1 hub.
			if peers, err := client.FetchPeers(ctx, bootstrap.PeerQuery{PreferredRegion: region, Role: "edge", Limit: 2}); err == nil {
				connect(peers)
			}
			if peers, err := client.FetchPeers(ctx, bootstrap.PeerQuery{PreferredRegion: region, Role: "hub", Limit: 2}); err == nil {
				connectFirst(peers)
			}

		case "hub":
			// 2 local peers + 2 same-region hubs + 1 cross-region hub.
			if peers, err := client.FetchPeers(ctx, bootstrap.PeerQuery{PreferredRegion: region, Limit: 2}); err == nil {
				connect(peers)
			}
			if peers, err := client.FetchPeers(ctx, bootstrap.PeerQuery{PreferredRegion: region, Role: "hub", Limit: 2}); err == nil {
				connect(peers)
			}
			// Cross-region: fetch hubs from any region, then client-side filter to other regions.
			if all, err := client.FetchPeers(ctx, bootstrap.PeerQuery{Role: "hub", AllowRemote: true, Limit: 5}); err == nil {
				var remote []bootstrap.Node
				for _, p := range all {
					if p.Region != region {
						remote = append(remote, p)
					}
				}
				connectFirst(remote)
			}

		default:
			// Flat discovery: any peers in the preferred region.
			if peers, err := client.FetchPeers(ctx, bootstrap.PeerQuery{PreferredRegion: region, Limit: 5}); err == nil {
				connect(peers)
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

func (s *Server) maintainPeer(ctx context.Context, peer Peer) {
	for {
		if ctx.Err() != nil {
			return
		}

		sess, err := s.MoQDialer.DialQUIC(ctx, peer.Address, s.TrackMux)
		if err != nil {
			slog.Warn("failed to dial peer", "address", peer.Address, "error", err)
			if !waitRetry(ctx, 5*time.Second) {
				return
			}
			continue
		}

		slog.Info("peer connected", "address", peer.Address)

		s.Relay(sess)

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

func (s *Server) Relay(sess *moqt.Session) {
	s.init()

	if s.statusHandler != nil {
		s.statusHandler.incrementConnections()
		defer s.statusHandler.decrementConnections()
	}

	slog.Info("relay: new session", "remote", sess.RemoteAddr())

	announced, err := sess.AcceptAnnounce("/")
	if err != nil {
		slog.Warn("failed to accept announcement", "error", err)
		return
	}

	for {
		ann, err := announced.ReceiveAnnouncement(context.Background())
		if err != nil {
			slog.Warn("relay: announcements loop ended",
				"remote", sess.RemoteAddr(),
				"error", err,
				"reader_ctx_err", announced.Context().Err(),
				"sess_ctx_err", sess.Context().Err())
			return
		}

		slog.Info("relay: received announcement",
			"broadcast_path", ann.BroadcastPath(),
			"remote", sess.RemoteAddr())

		handler := newRelayHandler(ann, sess)

		s.TrackMux.Announce(ann, handler)
		slog.Info("relay: registered on TrackMux",
			"broadcast_path", ann.BroadcastPath())
	}
}
