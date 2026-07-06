package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/cors"
	"github.com/qumo-dev/qumo/internal/rtsp"
)

// RunRTSPPull connects to an RTSP source (e.g. an IP camera), pulls the stream
// via DESCRIBE/SETUP/PLAY, and republishes it as MoQT so subscribers can
// consume it. It mirrors RunRTSP's MoQT-serve wiring (TrackMux + WebTransport
// + moqt.Server) but replaces the push-server with a pull-client that feeds the
// same Session, with automatic reconnect on failure.
//
// Configuration:
//
//	args[0]           RTSP source URL (rtsp://[user:pass@]host/path)
//	args[1] (opt)     broadcast path (default /live/camera)
//	RTSP_SERVE_ADDR   MoQT listen address (default :4433)
//	CERT_FILE         TLS certificate (default certs/server.crt)
//	KEY_FILE          TLS key (default certs/server.key)
//	CORS_ALLOWED_ORIGINS  comma-separated WebTransport origins
func RunRTSPPull(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qumo rtsp <rtsp-url> [broadcast-path]")
	}
	srcURL := args[0]
	path := "/live/camera"
	if len(args) > 1 {
		path = args[1]
	}
	serveAddr := envOr("RTSP_SERVE_ADDR", defaultRTMPServeAddr)
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")
	allowedOrigins := cors.LoadAllowed()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trackMux := moqt.NewTrackMux(0)

	sess, err := NewSession(trackMux, moqt.BroadcastPath(path))
	if err != nil {
		return fmt.Errorf("rtsp pull session: %w", err)
	}
	defer sess.Close()

	// MoQT origin (WebTransport/QUIC) — same wiring as RunRTSP.
	wtHandler := &moqt.WebTransportHandler{
		TrackMux:    trackMux,
		CheckOrigin: cors.NewChecker(allowedOrigins),
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())
			<-sess.Context().Done()
		}),
	}
	mux := http.NewServeMux()
	mux.Handle("/", wtHandler)
	moqSrv := &moqt.Server{
		Addr:               serveAddr,
		WebTransportServer: moqt.NewWebTransportServer(mux),
		TrackMux:           trackMux,
	}
	go func() {
		if err := moqSrv.ListenAndServeTLS(certFile, keyFile); err != nil && ctx.Err() == nil {
			slog.Error("MoQT server error", "err", err)
			cancel()
		}
	}()

	slog.Info("RTSP pull ingest starting",
		"source", redactURL(srcURL), "broadcast_path", path, "serve", serveAddr)

	// Pull loop with exponential backoff reconnect.
	go func() {
		backoff := 2 * time.Second
		for ctx.Err() == nil {
			err := pullStream(ctx, srcURL, sess)
			if ctx.Err() != nil {
				return
			}
			slog.Warn("RTSP pull disconnected, reconnecting", "error", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}()

	<-ctx.Done()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = moqSrv.Shutdown(shutCtx)
	return nil
}

// pullStream connects to the RTSP source, sets up all tracks, and reads
// interleaved RTP frames until an error occurs (connection drop, PLAY failure,
// or context cancellation). The caller handles reconnection.
func pullStream(ctx context.Context, srcURL string, sess *Session) error {
	client, err := rtsp.Dial(ctx, srcURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	sdp, err := client.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}

	// SETUP each supported media track on its own interleaved channel pair.
	channelToTrack := make(map[uint8]*rtspTrack)
	nextChan := 0
	for i := range sdp.Medias {
		media := &sdp.Medias[i]
		track := newRTSPTrackFromMedia(sess, media)
		if track.kind != trackKindVideo && track.kind != trackKindAudio {
			slog.Info("skipping unsupported RTSP media",
				"type", media.Type, "rtpmap", media.RtpMap)
			continue
		}
		trackURL := resolveControlURL(media.Control, srcURL)
		// Security: reject SDP control attributes that point to a different
		// origin (SSRF via malicious SDP). The resolved track URL must share
		// the session URL's scheme + host + port.
		if !sameOrigin(trackURL, srcURL) {
			slog.Warn("RTSP track control URL points to a different origin, skipping (possible SSRF)",
				"track_control", media.Control)
			continue
		}
		rtpCh := uint8(nextChan)
		rtcpCh := uint8(nextChan + 1)
		nextChan += 2
		setup, err := client.Setup(ctx, trackURL, rtpCh, rtcpCh)
		if err != nil {
			return fmt.Errorf("setup %s: %w", trackURL, err)
		}
		channelToTrack[setup.RTP] = track
		slog.Info("RTSP track set up",
			"type", media.Type, "rtpmap", media.RtpMap,
			"rtp_channel", setup.RTP, "rtcp_channel", setup.RTCP)
	}

	if len(channelToTrack) == 0 {
		return fmt.Errorf("no supported media tracks (need H.264 video and/or mpeg4-generic audio)")
	}

	if err := client.Play(ctx); err != nil {
		return fmt.Errorf("play: %w", err)
	}
	slog.Info("RTSP pull streaming started", "source", srcURL, "tracks", len(channelToTrack))

	for ctx.Err() == nil {
		frame, err := client.ReadInterleaved()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		track, ok := channelToTrack[frame.Channel]
		if !ok {
			continue // RTCP or unknown channel → discard.
		}
		track.handleFrame(frame)
	}
	return nil
}

// resolveControlURL resolves an SDP media's a=control URL against the session
// (DESCRIBE) URL. Handles absolute URLs (returned as-is), relative URLs
// (resolved against the session URL as a "directory"), and "*" or empty
// (returns the session URL).
func resolveControlURL(control, sessionURL string) string {
	if control == "" || control == "*" {
		return sessionURL
	}
	if strings.Contains(control, "://") {
		return control // absolute
	}
	base, err := url.Parse(sessionURL)
	if err != nil {
		return sessionURL
	}
	// Append "/" so ResolveReference treats the session path as a directory
	// (appends the relative control) rather than replacing the last segment.
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	ref, err := url.Parse(control)
	if err != nil {
		return sessionURL
	}
	return base.ResolveReference(ref).String()
}

// redactURL strips credentials (user:pass@) from a URL string so it is safe to
// log without leaking passwords.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL // best-effort: return as-is if unparseable
	}
	u.User = nil
	return u.String()
}

// sameOrigin reports whether two URLs share the same scheme, host, and port.
// Used to reject SDP a=control attributes that redirect to a different server
// (SSRF prevention).
func sameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && ua.Host == ub.Host
}
