package ingest

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

const (
	defaultRTMPIngestAddr = ":1935"
	defaultRTSPIngestAddr = ":8554"
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
//	CORS_ALLOWED_ORIGINS - comma-separated WebTransport origins (default: same-origin only; "*" allows any)
func RunRTMP(_ []string) error {
	ingestAddr := envOr("RTMP_INGEST_ADDR", defaultRTMPIngestAddr)
	serveAddr := envOr("RTMP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")
	allowedOrigins := loadAllowedOrigins()

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
		TrackMux:    trackMux,
		CheckOrigin: newOriginChecker(allowedOrigins),
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
//	RTSP_INGEST_ADDR     - RTSP listen address (default: ":8554")
//	RTSP_SERVE_ADDR      - MoQT listen address (default: ":4433")
//	CERT_FILE            - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE             - TLS key file (default: "certs/server.key")
//	CORS_ALLOWED_ORIGINS - comma-separated WebTransport origins (default: same-origin only; "*" allows any)
func RunRTSP(_ []string) error {
	ingestAddr := envOr("RTSP_INGEST_ADDR", defaultRTSPIngestAddr)
	serveAddr := envOr("RTSP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")
	allowedOrigins := loadAllowedOrigins()

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
		TrackMux:    trackMux,
		CheckOrigin: newOriginChecker(allowedOrigins),
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

// loadAllowedOrigins reads the comma-separated CORS_ALLOWED_ORIGINS environment
// variable consumed by the MoQT WebTransport origin.
func loadAllowedOrigins() []string {
	var out []string
	for o := range strings.SplitSeq(envOr("CORS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// newOriginChecker returns a WebTransport CheckOrigin callback that mitigates
// cross-site request forgery on session upgrades. A request is accepted when:
//   - it carries no Origin header (non-browser clients such as SDKs and CLIs),
//   - its Origin is listed in allowed, or allowed contains the wildcard "*",
//   - its Origin host matches the request Host (same-origin browser request).
//
// An empty allowed slice mirrors the underlying upgrader's default behaviour:
// only headerless and same-origin requests pass. This matches how the relay
// server configures its own WebTransportHandler (CheckOrigin left unset).
func newOriginChecker(allowed []string) func(*http.Request) bool {
	wildcard := slices.Contains(allowed, "*")
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allowedSet[o] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		if wildcard {
			return true
		}
		if _, ok := allowedSet[origin]; ok {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	}
}
