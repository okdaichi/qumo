package ingest

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

const (
	defaultRTMPIngestAddr = ":1935"
	defaultRTSPIngestAddr = ":554"
	defaultRTMPServeAddr  = ":4433"
)

// RunRTMP starts a standalone RTMP ingest server that bridges published
// streams to MoQT. Unlike the relay command this does not participate in
// the mesh (no peer connections, no announce relay).
//
// Configuration is read from environment variables:
//
//	RTMP_INGEST_ADDR    - RTMP listen address (default: ":1935")
//	RTMP_SERVE_ADDR     - MoQT listen address (default: ":4433")
//	CERT_FILE           - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE            - TLS key file (default: "certs/server.key")
func RunRTMP(_ []string) error {
	ingestAddr := envOr("RTMP_INGEST_ADDR", defaultRTMPIngestAddr)
	serveAddr := envOr("RTMP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trackMux := moqt.NewTrackMux(0)

	// RTMP ingest server
	rtmpSrv := NewRTMPServer(RTMPConfig{
		Addr:     ingestAddr,
		TrackMux: trackMux,
	})

	// WebTransportHandler upgrades HTTP/3 requests into MoQT sessions.
	wtHandler := &moqt.WebTransportHandler{
		TrackMux: trackMux,
		CheckOrigin: func(r *http.Request) bool {
			return true // allow cross-origin (Vite dev server)
		},
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())
			<-sess.Context().Done()
		}),
	}

	mux := http.NewServeMux()
	mux.Handle("/", wtHandler)

	// Minimal MoQT origin that serves subscribers from the shared TrackMux.
	moqtSrv := &moqt.Server{
		Addr:               serveAddr,
		WebTransportServer: moqt.NewWebTransportServer(mux),
		TrackMux:           trackMux,
	}

	log.Println("	Ingest  :", ingestAddr)
	log.Println("	Serve   :", serveAddr)

	// Start RTMP ingest
	go func() {
		if err := rtmpSrv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Error("RTMP server error", "err", err)
			cancel()
		}
	}()

	// Start MoQT origin (QUIC)
	go func() {
		if err := moqtSrv.ListenAndServeTLS(certFile, keyFile); err != nil && ctx.Err() == nil {
			slog.Error("MoQT server error", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	_ = rtmpSrv.Shutdown(shutdownCtx)
	_ = moqtSrv.Shutdown(shutdownCtx)

	return nil
}

// RunRTSP starts a standalone RTSP ingest server that bridges published
// streams to MoQT.
//
// Configuration is read from environment variables:
//
//	RTSP_INGEST_ADDR    - RTSP listen address (default: ":554")
//	RTSP_SERVE_ADDR     - MoQT listen address (default: ":4433")
//	CERT_FILE           - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE            - TLS key file (default: "certs/server.key")
func RunRTSP(_ []string) error {
	ingestAddr := envOr("RTSP_INGEST_ADDR", defaultRTSPIngestAddr)
	serveAddr := envOr("RTSP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trackMux := moqt.NewTrackMux(0)

	// RTSP ingest server
	rtspSrv := NewRTSPServer(RTSPConfig{
		Addr:     ingestAddr,
		TrackMux: trackMux,
	})

	// WebTransportHandler upgrades HTTP/3 requests into MoQT sessions.
	wtHandler := &moqt.WebTransportHandler{
		TrackMux: trackMux,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())
			<-sess.Context().Done()
		}),
	}

	mux := http.NewServeMux()
	mux.Handle("/", wtHandler)

	// Minimal MoQT origin
	moqtSrv := &moqt.Server{
		Addr:               serveAddr,
		WebTransportServer: moqt.NewWebTransportServer(mux),
		TrackMux:           trackMux,
	}

	log.Println("	Ingest  :", ingestAddr)
	log.Println("	Serve   :", serveAddr)

	// Start RTSP ingest
	go func() {
		if err := rtspSrv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Error("RTSP server error", "err", err)
			cancel()
		}
	}()

	// Start MoQT origin (QUIC)
	go func() {
		if err := moqtSrv.ListenAndServeTLS(certFile, keyFile); err != nil && ctx.Err() == nil {
			slog.Error("MoQT server error", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	_ = rtspSrv.Shutdown(shutdownCtx)
	_ = moqtSrv.Shutdown(shutdownCtx)

	return nil
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
