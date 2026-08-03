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

	"github.com/okdaichi/qumo-ledger/ledger"
	"github.com/okdaichi/qumo-ledger/ledger/store/fsstore"
	"github.com/okdaichi/qumo-ledger/stream"
)

// Run starts the HLS/DASH egress server: it feeds a MoQ track from a relay into
// a qumo-ledger track and serves the ledger's HLS/DASH renderings over HTTP.
//
// Configuration is read from environment variables (qumo convention):
//
//	HLS_ADDR           - HTTP listen address (default ":8080")
//	LEDGER_ROOT        - qumo-ledger filesystem store directory (default "./ledger")
//	LEDGER_TRACK       - ledger track path (default "live/cam1/video")
//	RELAY_URL          - MoQ relay URL, e.g. "https://host:4433" (default "https://localhost:4433")
//	RELAY_TRACK_PATH   - MoQ broadcast path to subscribe to (default "")
//	RELAY_TRACK_NAME   - MoQ track name to subscribe to (default "video")
//	TIMESCALE          - ledger track timescale units per second (default 90000)
//	GROUP_DURATION_MS  - assumed group duration for media-time derivation (default 2000)
//	RELAY_TLS_INSECURE - skip relay TLS verification, dev only (default "true")
func Run(_ []string) error {
	addr := envOr("HLS_ADDR", ":8080")
	root := envOr("LEDGER_ROOT", "./ledger")
	trackPath := ledger.TrackPath(envOr("LEDGER_TRACK", "live/cam1/video"))

	timescale, err := envUint("TIMESCALE", 90000)
	if err != nil {
		return fmt.Errorf("invalid TIMESCALE: %w", err)
	}
	groupMs, err := envUint("GROUP_DURATION_MS", 2000)
	if err != nil {
		return fmt.Errorf("invalid GROUP_DURATION_MS: %w", err)
	}
	durationUnits := int64(groupMs) * int64(timescale) / 1000

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := fsstore.New(root)
	if err != nil {
		return fmt.Errorf("hls: open ledger store: %w", err)
	}

	track, err := openTrack(ctx, store, trackPath, uint32(timescale))
	if err != nil {
		return err
	}

	writer, err := track.Writer(ctx)
	if err != nil {
		return fmt.Errorf("hls: open writer: %w", err)
	}

	handler, err := stream.NewHandler(track, stream.Options{})
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
		if err := feed(ctx, writer, feedConfig{
			relayURL:      envOr("RELAY_URL", "https://localhost:4433"),
			trackPath:     envOr("RELAY_TRACK_PATH", ""),
			trackName:     envOr("RELAY_TRACK_NAME", "video"),
			durationUnits: durationUnits,
			insecure:      envOr("RELAY_TLS_INSECURE", "true") == "true",
		}); err != nil {
			slog.Error("hls: feed ended", "err", err)
		}
	}()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("hls: http serve", "err", err)
			cancel()
		}
	}()

	slog.Info("hls: serving", "addr", addr, "track", trackPath)

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	// not actionable: a shutdown error only reports a closed listener on exit.
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}

// openTrack creates the ledger track if absent, adopting an existing one. The
// schema's TimeSource is ingest: the feed stamps Wallclock from its own clock.
func openTrack(ctx context.Context, store *fsstore.Store, path ledger.TrackPath, timescale uint32) (*ledger.Track, error) {
	track, err := ledger.Create(ctx, store, path, ledger.TrackSchema{
		Timescale:  timescale,
		TimeSource: ledger.TimeSourceIngest,
		MIME:       "video/mp4",
		Encoding:   "fmp4",
	}, ledger.Config{})
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
