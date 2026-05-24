package ingest

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

const (
	defaultRTMPIngestAddr = ":1935"
	defaultRTMPServeAddr  = ":4433"
)

// RunRTMP starts a standalone RTMP ingest server that bridges published
// streams to MoQT. Unlike the relay command this does not participate in
// the mesh (no peer connections, no announce relay).
//
// Configuration is read from environment variables:
//
//	RTMP_INGEST_ADDR     - RTMP listen address (default: ":1935")
//	RTMP_SERVE_ADDR      - MoQT listen address (default: ":4433")
//	CERT_FILE            - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE             - TLS key file (default: "certs/server.key")
//	CORS_ALLOWED_ORIGINS - Comma-separated list of allowed CORS origins (default: empty, same-origin only)
func RunRTMP(_ []string) error {
	ingestAddr := envOr("RTMP_INGEST_ADDR", defaultRTMPIngestAddr)
	serveAddr := envOr("RTMP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")
	allowedOriginsEnv := os.Getenv("CORS_ALLOWED_ORIGINS")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trackMux := moqt.NewTrackMux(0)

	// RTMP ingest server
	rtmpSrv := NewRTMPServer(RTMPConfig{
		Addr:     ingestAddr,
		TrackMux: trackMux,
	})

	var checkOriginFunc func(r *http.Request) bool
	if allowedOriginsEnv != "" {
		allowedOrigins := make(map[string]bool)
		for _, o := range strings.Split(allowedOriginsEnv, ",") {
			allowedOrigins[strings.TrimSpace(o)] = true
		}
		checkOriginFunc = func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return allowedOrigins[origin]
		}
	}

	// WebTransportHandler upgrades HTTP/3 requests into MoQT sessions.
	wtHandler := &moqt.WebTransportHandler{
		TrackMux:    trackMux,
		CheckOrigin: checkOriginFunc,
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

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
