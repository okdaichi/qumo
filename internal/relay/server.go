package relay

import (
	"context"
	"crypto/tls"
	"log/slog"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/quic-go/quic-go"
)

type Server struct {
	Addr       string
	TLSConfig  *tls.Config
	QUICConfig *quic.Config
	Config     *Config

	TrackMux *moqt.TrackMux

	server *moqt.Server

	initOnce sync.Once

	statusHandler *statusHandler
	peerRegistry  *peerRegistry
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		if s.TrackMux == nil {
			s.TrackMux = moqt.DefaultMux
		}

		s.statusHandler = newStatusHandler()
		s.peerRegistry = newPeerRegistry()
	})
}

func (s *Server) Status() Status {
	s.init()

	return s.statusHandler.getStatus()
}

// ListenAndServe starts the relay server.
func (s *Server) ListenAndServe() error {
	if s.TLSConfig == nil {
		panic("relay.Server: TLSConfig is required")
	}

	s.init()

	s.server = &moqt.Server{
		Addr:       s.Addr,
		TLSConfig:  s.TLSConfig,
		QUICConfig: s.QUICConfig,
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())

			err := s.relay(sess)
			if err != nil {
				slog.Warn("relay session ended", "err", err)
				return
			}
		}),
	}

	// Start server - this will block until server closes
	return s.server.ListenAndServe()
}

func (s *Server) Close() error {
	s.init()

	if s.server != nil {
		_ = s.server.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.init()

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}

	return nil
}

// ConnectPeers dials configured peer relays and discovers their announcements
// via ANNOUNCE_PLEASE. Received announcements are registered on the local
// TrackMux so that subscribers can transparently access remote content.
// It blocks until ctx is cancelled.
func (s *Server) ConnectPeers(ctx context.Context) {
	s.init()

	peers := s.Config.Peers
	if len(peers) == 0 {
		return
	}

	dialer := &moqt.Dialer{
		TLSConfig:  s.TLSConfig,
		QUICConfig: s.QUICConfig,
	}

	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.maintainPeer(ctx, dialer, peer)
		}()
	}

	wg.Wait()
}

// maintainPeer keeps a connection to a peer alive, reconnecting on failure.
func (s *Server) maintainPeer(ctx context.Context, dialer *moqt.Dialer, peer Peer) {
	for {
		if ctx.Err() != nil {
			return
		}

		slog.Info("connecting to peer", "address", peer.Address)

		sess, err := dialer.Dial(ctx, peer.Address, s.TrackMux)
		if err != nil {
			slog.Warn("failed to dial peer", "address", peer.Address, "error", err)
			if !waitRetry(ctx, 5*time.Second) {
				return
			}
			continue
		}

		slog.Info("peer connected", "address", peer.Address)

		err = s.relay(sess)
		if err != nil {
			slog.Warn("peer session ended", "address", peer.Address, "error", err)
		}
		sess.CloseWithError(moqt.NoError, moqt.NoError.String())

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

func (s *Server) relay(sess *moqt.Session) error {
	slog.Info("session established", "remote", sess.RemoteAddr())
	defer slog.Info("session closed", "remote", sess.RemoteAddr())

	if s.statusHandler != nil {
		s.statusHandler.incrementConnections()
		defer s.statusHandler.decrementConnections()
	}

	if s.peerRegistry != nil {
		peerID := s.peerRegistry.register(sess)
		defer s.peerRegistry.deregister(peerID)
	}

	announced, err := sess.AcceptAnnounce("/")
	if err != nil {
		return err
	}

	for ann := range announced.Announcements(context.Background()) {
		handler := newRelayHandler(ann, sess)

		s.TrackMux.Announce(ann, handler)
	}

	return nil
}
