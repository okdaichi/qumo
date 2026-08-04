package hls

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/msf"

	"github.com/okdaichi/qumo-ledger/ledger"
)

// feedConfig carries the relay subscription parameters, resolved from
// environment by [Run].
type feedConfig struct {
	relayURL  string // relay URL, e.g. "https://host:4433"
	trackPath string // MoQ broadcast path (catalog and media track live under it)
	trackName string // MoQ track name to relay, selected from the catalog
	groupMs   uint64 // assumed group extent, for media-time derivation
	insecure  bool   // skip relay TLS verification
}

// mediaInfo is the selected media track's identity and ledger projection,
// learned from the MSF catalog: schema drives the ledger track, and init is the
// fMP4 initialization segment carried in the catalog's initData.
type mediaInfo struct {
	path   moqt.BroadcastPath
	name   moqt.TrackName
	schema ledger.TrackSchema
	init   []byte // fMP4 init bytes (from the catalog InitData); nil if none
}

// connect dials the relay, reads the MSF catalog, and returns the selected
// media track's identity, ledger schema, and fMP4 init. The session stays open
// for [feedMedia] to subscribe on.
func connect(ctx context.Context, cfg feedConfig, fallbackTimescale uint32) (*moqt.Session, mediaInfo, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS13}
	if cfg.insecure {
		tc.InsecureSkipVerify = true
	}
	session, err := (&moqt.Dialer{TLSConfig: tc}).Dial(ctx, cfg.relayURL, moqt.NewTrackMux(0))
	if err != nil {
		return nil, mediaInfo{}, fmt.Errorf("hls: dial relay: %w", err)
	}

	catalog, err := fetchCatalog(ctx, session, cfg.trackPath)
	if err != nil {
		// not actionable: the close outcome is irrelevant once the catalog read failed.
		_ = session.CloseWithError(moqt.NoError, "catalog fetch failed")
		return nil, mediaInfo{}, err
	}

	track := findTrack(catalog, cfg.trackName)
	if track == nil {
		_ = session.CloseWithError(moqt.NoError, "track not found")
		return nil, mediaInfo{}, fmt.Errorf("hls: track %q not in catalog", cfg.trackName)
	}

	schema := schemaFromTrack(track, fallbackTimescale)
	slog.Info("hls: catalog loaded",
		"track", track.Name, "packaging", track.Packaging, "timescale", schema.Timescale)

	return session, mediaInfo{
		path:   moqt.BroadcastPath(cfg.trackPath),
		name:   moqt.TrackName(cfg.trackName),
		schema: schema,
		init:   initFromTrack(track),
	}, nil
}

// fetchCatalog subscribes to the reserved catalog track and parses its first
// group as the MSF catalog.
func fetchCatalog(ctx context.Context, session *moqt.Session, path string) (msf.Catalog, error) {
	cat, err := session.Subscribe(ctx,
		moqt.BroadcastPath(path),
		moqt.TrackName(msf.DefaultCatalogTrackName),
		&moqt.SubscribeConfig{Ordered: true},
	)
	if err != nil {
		return msf.Catalog{}, fmt.Errorf("hls: subscribe catalog: %w", err)
	}
	defer cat.Close()

	gr, err := cat.AcceptGroup(ctx)
	if err != nil {
		return msf.Catalog{}, fmt.Errorf("hls: read catalog group: %w", err)
	}
	data, _, err := drainGroup(gr, moqt.NewFrame(0))
	if err != nil {
		return msf.Catalog{}, fmt.Errorf("hls: read catalog payload: %w", err)
	}

	catalog, err := msf.ParseCatalog(data)
	if err != nil {
		return msf.Catalog{}, fmt.Errorf("hls: parse catalog: %w", err)
	}
	return catalog, nil
}

// findTrack returns the named track in the catalog, or nil.
func findTrack(c msf.Catalog, name string) *msf.Track {
	for i := range c.Tracks {
		if c.Tracks[i].Name == name {
			return &c.Tracks[i]
		}
	}
	return nil
}

// schemaFromTrack projects an MSF catalog track onto a ledger [ledger.TrackSchema].
// The packaging is CMAF (fMP4); Timescale and MIME come from the catalog when
// present, else from the fallback.
func schemaFromTrack(t *msf.Track, fallbackTimescale uint32) ledger.TrackSchema {
	s := ledger.TrackSchema{
		Timescale:  fallbackTimescale,
		TimeSource: ledger.TimeSourceIngest,
		MIME:       "video/mp4",
		Encoding:   "fmp4",
	}
	if t.Timescale != nil {
		s.Timescale = uint32(*t.Timescale)
	}
	if t.MimeType != "" {
		s.MIME = t.MimeType
	}
	return s
}

// initFromTrack base64-decodes the track's InitData (the fMP4 init), returning
// nil when the track carries none or it is malformed.
func initFromTrack(t *msf.Track) []byte {
	if t.InitData == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(t.InitData)
	if err != nil {
		return nil
	}
	return b
}

// feedMedia subscribes to the media track and relays each received group into
// the ledger as a sealed group. It blocks until ctx is cancelled or the
// subscription fails.
//
// Each group's payload is treated as one CMAF (fMP4) fragment — the bytes the
// publisher packaged — so the ledger segment is what the player fetches.
// MediaTime/Duration are still derived (gomoqt v0.15.0 carries no per-frame
// timestamp); the Timescale now comes from the catalog.
func feedMedia(ctx context.Context, session *moqt.Session, m mediaInfo, w *ledger.Writer, cfg feedConfig) error {
	tr, err := session.Subscribe(ctx, m.path, m.name, &moqt.SubscribeConfig{Ordered: true})
	if err != nil {
		return fmt.Errorf("hls: subscribe %s: %w", m.name, err)
	}
	defer tr.Close()

	slog.Info("hls: subscribed", "path", m.path, "name", m.name)

	durationUnits := int64(cfg.groupMs) * int64(m.schema.Timescale) / 1000
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
			slog.Warn("hls: read group, skipping", "group", gr.GroupSequence(), "err", err)
			continue
		}

		info := groupInfo(uint64(gr.GroupSequence()), index, durationUnits, objects, time.Now())
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
