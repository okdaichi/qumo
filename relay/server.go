package relay

import (
	"context"
	"crypto/tls"
	"log"
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

	client *moqt.Client
	server *moqt.Server

	initOnce sync.Once
}

func (s *Server) init() {
	s.initOnce.Do(func() {
		if s.TLSConfig == nil {
			panic("no tls config")
		}

		if s.TrackMux == nil {
			s.TrackMux = moqt.DefaultMux
		}
	})
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

			err = Relay(ctx, downstream, s.TrackMux)

			if err != nil {
				slog.Error("relay session ended", "err", err)
				return
			}
		}),
	}

	var wg sync.WaitGroup

	// Only connect to upstream if URL is provided
	if s.Config.Upstream != "" {
		s.client = &moqt.Client{
			TLSConfig:  s.TLSConfig,
			QUICConfig: s.QUICConfig,
		}

		wg.Go(func() {
			upstream, err := s.client.Dial(ctx, s.Config.Upstream, s.TrackMux)
			if err != nil {
				log.Printf("Failed to connect to upstream: %v", err)
				return
			}
			log.Printf("Connected to upstream: %s", s.Config.Upstream)

			defer upstream.CloseWithError(moqt.NoError, moqt.SessionErrorText(moqt.NoError))

			err = Relay(ctx, upstream, s.TrackMux)
			if err != nil {
				return
			}

		})
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		err := s.server.HandleWebTransport(w, r)
		if err != nil {
			slog.Error("failed to handle web transport", "err", err)
		}
	})

	// Start server - this will block until server closes
	err := s.server.ListenAndServe()

	// Wait for upstream goroutine to finish (if it was started)
	wg.Wait()

	return err
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

	if s.client != nil {
		done := make(chan error, 1)
		go func() {
			done <- s.client.Shutdown(ctx)
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
