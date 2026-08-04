package hls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"

	"github.com/okdaichi/qumo-ledger/ledger"
	"github.com/okdaichi/qumo-ledger/ledger/store/fsstore"
	"github.com/okdaichi/qumo-ledger/stream"
)

// Run starts the HLS/DASH egress server: it subscribes to a MoQ track's catalog
// to learn its schema and fMP4 init, writes each received CMAF group into a
// qumo-ledger track, and serves the ledger's HLS/DASH renderings over HTTP.
//
// Configuration is read from environment variables (qumo convention):
//
//	HLS_ADDR           - HTTP listen address (default ":8080")
//	LEDGER_ROOT        - qumo-ledger filesystem store directory (default "./ledger")
//	LEDGER_TRACK       - ledger track path (default "live/cam1/video")
//	RELAY_URL          - MoQ relay URL, e.g. "https://host:4433" (default "https://localhost:4433")
//	RELAY_TRACK_PATH   - MoQ broadcast path whose catalog to read (default "/live/cam1")
//	RELAY_TRACK_NAME   - media track name in the catalog to relay (default "video")
//	TIMESCALE          - fallback timescale when the catalog omits it (default 90000)
//	GROUP_DURATION_MS  - assumed group duration for media-time derivation (default 2000)
//	RELAY_TLS_INSECURE - skip relay TLS verification, dev only (default "true")
func Run(_ []string) error {
	addr := envOr("HLS_ADDR", ":8080")
	root := envOr("LEDGER_ROOT", "./ledger")
	ledgerTrack := ledger.TrackPath(envOr("LEDGER_TRACK", "live/cam1/video"))

	fallbackTimescale, err := envUint("TIMESCALE", 90000)
	if err != nil {
		return fmt.Errorf("invalid TIMESCALE: %w", err)
	}
	groupMs, err := envUint("GROUP_DURATION_MS", 2000)
	if err != nil {
		return fmt.Errorf("invalid GROUP_DURATION_MS: %w", err)
	}

	cfg := feedConfig{
		relayURL:  envOr("RELAY_URL", "https://localhost:4433"),
		trackPath: envOr("RELAY_TRACK_PATH", "/live/cam1"),
		trackName: envOr("RELAY_TRACK_NAME", "video"),
		groupMs:   groupMs,
		insecure:  envOr("RELAY_TLS_INSECURE", "true") == "true",
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := fsstore.New(root)
	if err != nil {
		return fmt.Errorf("hls: open ledger store: %w", err)
	}

	// Read the catalog before opening the track or building the handler: the
	// schema and fMP4 init come from the catalog.
	session, media, err := connect(ctx, cfg, uint32(fallbackTimescale))
	if err != nil {
		return err
	}
	defer func() {
		// not actionable: the egress is stopping regardless of the close outcome.
		_ = session.CloseWithError(moqt.NoError, "hls egress stopped")
	}()

	track, err := openTrack(ctx, store, ledgerTrack, media.schema)
	if err != nil {
		return err
	}
	writer, err := track.Writer(ctx)
	if err != nil {
		return fmt.Errorf("hls: open writer: %w", err)
	}

	opts := stream.Options{}
	if len(media.init) > 0 {
		opts.InitSegment = stream.InitSegment{Bytes: media.init}
	}
	handler, err := stream.NewHandler(track, opts)
	if err != nil {
		return fmt.Errorf("hls: build stream handler: %w", err)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// The feed writes MoQ groups into the ledger; it is independent of the HTTP
	// server so a feed failure leaves the already-recorded track replayable.
	go func() {
		if err := feedMedia(ctx, session, media, writer, cfg); err != nil && ctx.Err() == nil {
			slog.Error("hls: feed ended", "err", err)
		}
	}()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("hls: http serve", "err", err)
			cancel()
		}
	}()

	slog.Info("hls: serving", "addr", addr, "track", ledgerTrack)

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	// not actionable: a shutdown error only reports a closed listener on exit.
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}

// openTrack creates the ledger track if absent, adopting an existing one. The
// schema comes from the MSF catalog (its TimeSource is ingest: the feed stamps
// Wallclock from its own clock).
func openTrack(ctx context.Context, store *fsstore.Store, path ledger.TrackPath, schema ledger.TrackSchema) (*ledger.Track, error) {
	track, err := ledger.Create(ctx, store, path, schema, ledger.Config{})
	if err != nil {
		if errors.Is(err, ledger.ErrTrackExists) {
			return ledger.Open(ctx, store, path, ledger.Config{})
		}
		return nil, fmt.Errorf("hls: create track: %w", err)
	}
	return track, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envUint(key string, def uint64) (uint64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}
