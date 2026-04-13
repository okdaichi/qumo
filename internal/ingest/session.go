// Package ingest bridges media publish protocols (RTMP, SRT, WHIP, etc.)
// to MoQT. Each protocol adapter accepts incoming connections, extracts
// audio/video frames, and pushes them into a protocol-agnostic [Session].
// The Session announces the media on a [moqt.TrackMux] so that MoQT
// subscribers can consume the stream.
//
// # Adding a new protocol
//
// Implement a listener that accepts connections for the target protocol,
// create a [Session] per publish stream, and push frames:
//
//	sess := ingest.NewSession(trackMux, "/app/stream")
//	defer sess.Close()
//	for {
//	    ts, data, isKey := readFromProtocol()
//	    sess.PushVideo(ts, data, isKey)
//	}
package ingest

import (
	"context"
	"log/slog"

	"github.com/okdaichi/gomoqt/moqt"
)

// Session manages the MoQT announcement and fan-out for a single ingested
// publish stream, regardless of the source protocol. Protocol adapters
// create a Session via [NewSession] and push media data through
// [Session.PushVideo] and [Session.PushAudio].
//
// A Session is safe for concurrent use; however, callers should typically
// push video and audio from a single goroutine per track.
type Session struct {
	path    moqt.BroadcastPath
	handler *ingestHandler
	endAnn  func()
	cancel  context.CancelFunc
}

// NewSession creates a new ingest session that announces path on trackMux.
// Subscribers that request tracks under this broadcast path will receive
// the media pushed via [Session.PushVideo] and [Session.PushAudio].
//
// Call [Session.Close] when the publisher disconnects.
func NewSession(trackMux *moqt.TrackMux, path moqt.BroadcastPath) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	ann, endAnn := moqt.NewAnnouncement(ctx, path)

	h := newIngestHandler()
	trackMux.Announce(ann, h)

	slog.Info("ingest session started", "broadcast_path", path)

	return &Session{
		path:    path,
		handler: h,
		endAnn:  endAnn,
		cancel:  cancel,
	}
}

// PushVideo appends converted video data (Annex-B bitstream) as a MoQT
// frame wrapped in a MediaFrame envelope. When isKeyframe is true a new
// MoQT group is opened (GOP boundary).
//
// timestampUS is the presentation timestamp in microseconds.
func (s *Session) PushVideo(timestampUS int64, data []byte, isKeyframe bool) {
	payload := buildMediaFrame(timestampUS, data)
	f := moqt.NewFrame(len(payload))
	f.Write(payload)
	s.handler.video.pushVideo(f, isKeyframe)
}

// PushAudio appends converted audio data (raw AAC frame) as an
// independently-decodable MoQT group wrapped in a MediaFrame envelope.
//
// timestampUS is the presentation timestamp in microseconds.
func (s *Session) PushAudio(timestampUS int64, data []byte) {
	payload := buildMediaFrame(timestampUS, data)
	f := moqt.NewFrame(len(payload))
	f.Write(payload)
	s.handler.audio.pushAudio(f)
}

// PublishCatalog publishes the MSF catalog as a single-frame group on the
// catalog track so that subscribers can discover track metadata (codec,
// resolution, sample rate, etc.).
func (s *Session) PublishCatalog(catalogJSON []byte) {
	f := moqt.NewFrame(len(catalogJSON))
	f.Write(catalogJSON)
	s.handler.catalog.pushCatalog(f)
}

// Close ends the MoQT announcement and signals all subscribers that
// the publisher has disconnected. It is safe to call multiple times.
func (s *Session) Close() {
	s.handler.close()
	s.endAnn()
	s.cancel()
	slog.Info("ingest session ended", "broadcast_path", s.path)
}
