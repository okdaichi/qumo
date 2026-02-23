package relay

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"sync"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/gomoqt/quic"
)

type Server struct {
	Addr       string
	TLSConfig  *tls.Config
	QUICConfig *quic.Config
	Config     *Config

	CheckHTTPOrigin func(r *http.Request) bool

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
		if s.TLSConfig == nil {
			panic("no tls config")
		}

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

func (s *Server) ListenAndServe() error {
	s.init()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.server = &moqt.Server{
		Addr:            s.Addr,
		TLSConfig:       s.TLSConfig,
		QUICConfig:      s.QUICConfig,
		CheckHTTPOrigin: s.CheckHTTPOrigin,
		SetupHandler: moqt.SetupHandlerFunc(func(w moqt.SetupResponseWriter, r *moqt.SetupRequest) {
			downstream, err := moqt.Accept(w, r, s.TrackMux)
			if err != nil {
				slog.Error("failed to accept connection", "err", err)
				return
			}

			defer downstream.CloseWithError(moqt.NoError, moqt.SessionErrorText(moqt.NoError))

			err = s.Relay(ctx, downstream)

			if err != nil {
				slog.Error("relay session ended", "err", err)
				return
			}
		}),
	}

	// Start server - this will block until server closes
	return s.server.ListenAndServe()
}

func (s *Server) HandleWebTransport(w http.ResponseWriter, r *http.Request) error {
	s.init()

	return s.server.HandleWebTransport(w, r)
}

func (s *Server) Close() error {
	//
	s.init()

	if s.server != nil {
		_ = s.server.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	//
	s.init()

	if s.server != nil {
		done := make(chan error, 1)
		go func() {
			done <- s.server.Shutdown(ctx)
		}()

		select {
		case err := <-done:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (s *Server) Relay(ctx context.Context, sess *moqt.Session) error {
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
	peer, err := sess.AcceptAnnounce("/")
	if err != nil {
		return err
	}

	for ann := range peer.Announcements(ctx) {
		// Push to SDN announce table if configured
		if s.AnnounceRegistrar != nil {
			s.AnnounceRegistrar.Register(string(ann.BroadcastPath()))
		}

		handler := &RelayHandler{
			Announcement:   ann,
			Session:        sess,
			GroupCacheSize: DefaultGroupCacheSize,
			FramePool:      DefaultFramePool,
			relaying:       make(map[moqt.TrackName]*trackDistributor),
		}

		s.TrackMux.Announce(ann, handler)
	}

	return nil
}
