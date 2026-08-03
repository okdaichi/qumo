package hls

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"

	"github.com/okdaichi/qumo-ledger/ledger"
)

// feedConfig carries the relay subscription parameters the feed goroutine
// needs, resolved from environment by [Run].
type feedConfig struct {
	relayURL      string // relay URL, e.g. "https://host:4433"
	trackPath     string // MoQ broadcast path
	trackName     string // MoQ track name
	durationUnits int64  // assumed group extent, in the track's timescale
	insecure      bool   // skip relay TLS verification
}

// feed subscribes to a MoQ track on a relay and writes each received group into
// the ledger as a sealed group. It blocks until ctx is cancelled or the
// subscription fails.
//
// Two things are deliberately approximate and flagged for the feature work:
//
//   - The group payload is the raw concatenation of MoQ frame bodies, not an
//     fMP4 segment. HLS playback needs a packaging step.
//   - gomoqt v0.15.0 exposes no per-frame media timestamp, so a group's
//     MediaTime and Duration come from feedConfig (a configured group extent)
//     and its Wallclock from the clock.
func feed(ctx context.Context, w *ledger.Writer, cfg feedConfig) error {
	tc := &tls.Config{MinVersion: tls.VersionTLS13}
	if cfg.insecure {
		tc.InsecureSkipVerify = true
	}

	session, err := (&moqt.Dialer{TLSConfig: tc}).Dial(ctx, cfg.relayURL, moqt.NewTrackMux(0))
	if err != nil {
		return fmt.Errorf("hls: dial relay: %w", err)
	}
	defer func() {
		// not actionable: the feed is ending regardless of how the close goes.
		_ = session.CloseWithError(moqt.NoError, "hls feed stopped")
	}()

	tr, err := session.Subscribe(ctx,
		moqt.BroadcastPath(cfg.trackPath),
		moqt.TrackName(cfg.trackName),
		&moqt.SubscribeConfig{Ordered: true},
	)
	if err != nil {
		return fmt.Errorf("hls: subscribe %s/%s: %w", cfg.trackPath, cfg.trackName, err)
	}
	defer tr.Close()

	slog.Info("hls: subscribed",
		"relay", cfg.relayURL, "path", cfg.trackPath, "name", cfg.trackName)

	frame := moqt.NewFrame(0)
	var index int64
	for {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("hls: accept group: %w", err)
		}

		payload, objects, err := drainGroup(gr, frame)
		if err != nil {
			// A read failure on one group is not fatal: skip it and keep feeding.
			slog.Warn("hls: read group, skipping", "group", gr.GroupSequence(), "err", err)
			continue
		}

		info := groupInfo(uint64(gr.GroupSequence()), index, cfg.durationUnits, objects, time.Now())
		if _, err := w.AppendGroup(ctx, info, payload); err != nil {
			// A sealed group is immutable; a duplicate or ordering refusal is
			// skipped rather than stopping the feed.
			slog.Warn("hls: append group, skipping", "group", info.ID, "err", err)
			continue
		}
		index++
	}
}

// drainGroup reads every frame of a group, returning the concatenated payload
// and the frame count. io.EOF ends the group cleanly.
func drainGroup(gr *moqt.GroupReader, frame *moqt.Frame) ([]byte, uint64, error) {
	var payload []byte
	var n uint64
	for {
		if err := gr.ReadFrame(frame); err != nil {
			if err == io.EOF {
				return payload, n, nil
			}
			return nil, 0, err
		}
		payload = append(payload, frame.Body()...)
		n++
	}
}

// groupInfo maps a MoQ group to a ledger [ledger.GroupInfo]. seq is the group's
// producer sequence (its identity, carried in the ID; the writer stamps the
// epoch); index is the append ordinal, which drives a monotonic media time; and
// durationUnits is the group's extent in the track's timescale.
//
// Media time follows the ordinal rather than the producer sequence because MoQ
// sequences are gappy — a dropped group must not advance the timeline.
func groupInfo(seq uint64, index int64, durationUnits int64, objectCount uint64, now time.Time) ledger.GroupInfo {
	return ledger.GroupInfo{
		ID:          ledger.NewGroupID(0, seq),
		MediaTime:   index * durationUnits,
		Duration:    durationUnits,
		Wallclock:   now.UnixNano(),
		ObjectCount: objectCount,
	}
}
