package relay

import (
	"context"
	"crypto/tls"
	"log"
	"sync"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/gomoqt/quic"
	"github.com/okdaichi/qumo/relay/health"
)

type Server struct {
	Addr       string
	TLSConfig  *tls.Config
	QUICConfig *quic.Config
	Config     *Config

	Client *moqt.Client

	TrackMux *moqt.TrackMux

	clientTrackMux *moqt.TrackMux

	client *moqt.Client
	server *moqt.Server

	// Health check support (optional)
	Health *health.StatusHandler

	initOnce sync.Once
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		if s.Config == nil {
			s.Config = &Config{}
		}

		if s.TLSConfig == nil {
			panic("no tls config")
		}

		if s.TrackMux == nil {
			s.TrackMux = moqt.DefaultMux
		}

		s.clientTrackMux = moqt.NewTrackMux()
	})
}

func (s *Server) ListenAndServe() error {
	s.init()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverMux := s.TrackMux
	clientMux := s.clientTrackMux

	s.server = &moqt.Server{
		Addr:       s.Addr,
		TLSConfig:  s.TLSConfig,
		QUICConfig: s.QUICConfig,
		SetupHandler: moqt.SetupHandlerFunc(func(w moqt.SetupResponseWriter, r *moqt.SetupRequest) {
			downstream, err := moqt.Accept(w, r, serverMux)
			if err != nil {
				// TODO: log error
				return
			}

			// Track connection
			s.Health.IncrementConnections()
			defer func() {
				downstream.CloseWithError(moqt.NoError, moqt.SessionErrorText(moqt.NoError))
				s.Health.DecrementConnections()
			}()

			err = Relay(ctx, downstream, func(handler *RelayHandler) {
				// Announce to downstream peers with server mux
				serverMux.Announce(handler.Announcement, handler)
				// Announce to upstream with client mux
				clientMux.Announce(handler.Announcement, handler)
			})

			if err != nil {
				// TODO: log error
				return
			}
		}),
	}

	s.client = &moqt.Client{
		TLSConfig:  s.TLSConfig,
		QUICConfig: s.QUICConfig,
	}

	go func() {
		upstream, err := s.client.Dial(ctx, s.Config.Upstream, clientMux)
		if err != nil {
			log.Printf("Failed to connect to upstream: %v", err)
			s.Health.SetUpstreamConnected(false)
			return
		}
		s.Health.SetUpstreamConnected(true)
		log.Printf("Connected to upstream: %s", s.Config.Upstream)

		defer func() {
			upstream.CloseWithError(moqt.NoError, moqt.SessionErrorText(moqt.NoError))
			s.Health.SetUpstreamConnected(false)
		}()

		err = Relay(ctx, upstream, func(handler *RelayHandler) {
			// Announce to downstream peers with server mux
			serverMux.Announce(handler.Announcement, handler)
		})
		if err != nil {
			return
		}

	}()

	return s.server.ListenAndServe()
}

func (s *Server) Close() error {
	//
	s.init()

	if s.server != nil {
		_ = s.server.Close()
	}

	if s.client != nil {
		_ = s.client.Close()
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	//
	s.init()

	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			return err
		}
	}

	if s.client != nil {
		if err := s.client.Shutdown(ctx); err != nil {
			return err
		}
	}

	return nil
}
