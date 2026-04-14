package relay

import (
	"context"
	"crypto/tls"
	"log/slog"
	"sync"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/quic-go/quic-go"
)

type Server struct {
	Addr       string
	TLSConfig  *tls.Config
	QUICConfig *quic.Config
	Config     *Config

	// CheckHTTPOrigin func(r *http.Request) bool

	TrackMux *moqt.TrackMux

	// AnnounceRegistrar pushes announcements to the SDN controller.
	// If nil, auto-announce is disabled.
	AnnounceRegistrar AnnounceRegistrar

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

// func (s *Server) HandleWebTransport(w http.ResponseWriter, r *http.Request) error {
// 	s.init()

// 	if s.server == nil {
// 		return fmt.Errorf("relay.Server: ListenAndServe has not been called")
// 	}

// 	return s.server.HandleWebTransport(w, r)
// }

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
		// s.server.Shutdown already respects ctx cancellation.
		return s.server.Shutdown(ctx)
	}

	return nil
}

func (s *Server) relay(sess *moqt.Session) error {
	slog.Info("session established", "remote", sess.RemoteAddr())
	defer slog.Info("session closed", "remote", sess.RemoteAddr())

	if s.statusHandler != nil {
		s.statusHandler.incrementConnections()
		defer s.statusHandler.decrementConnections()
	}

	// Register peer for topology tracking
	if s.peerRegistry != nil {
		peerID := s.peerRegistry.register(sess)
		defer s.peerRegistry.deregister(peerID)
	}

	// TODO: measure accept time
	announced, err := sess.AcceptAnnounce("/")
	if err != nil {
		return err
	}

	for ann := range announced.Announcements(context.Background()) {
		// Push to SDN announce table if configured
		if s.AnnounceRegistrar != nil {
			s.AnnounceRegistrar.Register(string(ann.BroadcastPath()))
		}

		handler := newRelayHandler(ann, sess)
		// Announcement is already provided above; other fields defaulted appropriately.

		s.TrackMux.Announce(ann, handler)
	}

	return nil
}
