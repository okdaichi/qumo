package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/bootstrap"
	"github.com/quic-go/quic-go"
)

type Server struct {
	Addr               string
	TLSConfig          *tls.Config
	QUICConfig         *quic.Config
	Config             *Config
	TrackMux           *moqt.TrackMux
	Logger             *slog.Logger
	WebTransportServer moqt.WebTransportServer

	moqServer *moqt.Server

	moqDialer *moqt.Dialer

	webtransportHandler *moqt.WebTransportHandler

	statusHandler *statusHandler
}

func (s *Server) ServeHelth(w http.ResponseWriter, r *http.Request) {
	// single handler that supports probes via query param: ?probe=live|ready
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	probe := r.URL.Query().Get("probe")

	switch probe {
	case "live":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
		return

	case "ready":
		status := s.statusHandler.getStatus()
		activeConns := status.ActiveConnections

		ready := true
		reason := "ready"

		if activeConns < 0 {
			ready = false
			reason = "invalid_connection_state"
		}

		statusCode := http.StatusOK
		if !ready {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if r.Method == http.MethodHead {
			return
		}

		response := map[string]any{"ready": ready}
		if !ready {
			response["reason"] = reason
		}
		_ = json.NewEncoder(w).Encode(response)
		return

	default:
		// full status
		status := s.statusHandler.getStatus()

		ready := true
		reason := "ready"
		if status.ActiveConnections < 0 {
			ready = false
			reason = "invalid_connection_state"
		}

		response := map[string]any{
			"status":             status.Status,
			"timestamp":          status.Timestamp,
			"uptime":             status.Uptime,
			"active_connections": status.ActiveConnections,
			"live":               true,
			"ready":              ready,
		}
		if !ready {
			response["ready_reason"] = reason
		}

		statusCode := http.StatusOK
		if status.Status == "unhealthy" {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}
}

func (s *Server) HandleWebTransport(w http.ResponseWriter, r *http.Request) {
	s.webtransportHandler.ServeHTTP(w, r)
}

func (s *Server) init() {
	if s.TrackMux == nil {
		s.TrackMux = moqt.DefaultMux
	}

	s.moqServer = &moqt.Server{
		Addr:               s.Addr,
		TLSConfig:          s.TLSConfig,
		QUICConfig:         s.QUICConfig,
		Handler:            moqt.HandleFunc(s.Relay),
		WebTransportServer: moqt.NewWebTransportServer(nil),
		Logger:             s.Logger,
	}

	s.moqDialer = &moqt.Dialer{
		TLSConfig:  s.TLSConfig,
		QUICConfig: s.QUICConfig,
	}

	s.webtransportHandler = &moqt.WebTransportHandler{
		TrackMux: s.TrackMux,
		Handler:  moqt.HandleFunc(s.Relay),
		Logger:   s.Logger,
	}
}

// ListenAndServe starts the relay server.
func (s *Server) ListenAndServe() error {
	if s.TLSConfig == nil {
		panic("relay.Server: TLSConfig is required")
	}

	s.init()

	// Start server - this will block until server closes
	return s.moqServer.ListenAndServe()
}

func (s *Server) Close() error {
	if s.moqServer != nil {
		_ = s.moqServer.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.moqServer != nil {
		return s.moqServer.Shutdown(ctx)
	}

	return nil
}

// ConnectPeers dials configured peer relays and discovers their announcements
// via ANNOUNCE_PLEASE. Received announcements are registered on the local
// TrackMux so that subscribers can transparently access remote content.
// It also starts bootstrap clients for each configured bootstrap server.
// It blocks until ctx is cancelled.
func (s *Server) ConnectPeers(ctx context.Context) {
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

// maintainPeer keeps a connection to a peer alive, reconnecting on failure.
func (s *Server) maintainPeer(ctx context.Context, peer Peer) {
	for {
		if ctx.Err() != nil {
			return
		}

		slog.Info("connecting to peer", "address", peer.Address)

		sess, err := s.moqDialer.Dial(ctx, peer.Address, s.TrackMux)
		if err != nil {
			slog.Warn("failed to dial peer", "address", peer.Address, "error", err)
			if !waitRetry(ctx, 5*time.Second) {
				return
			}
			continue
		}

		slog.Info("peer connected", "address", peer.Address)

		s.Relay(sess)

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
	defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())

	slog.Info("session established", "remote", sess.RemoteAddr())
	defer slog.Info("session closed", "remote", sess.RemoteAddr())

	announced, err := sess.AcceptAnnounce("/")
	if err != nil {
		slog.Warn("failed to accept announcement", "error", err)
		return
	}

	for ann := range announced.Announcements(context.Background()) {
		handler := newRelayHandler(ann, sess)

		s.TrackMux.Announce(ann, handler)
	}
}
