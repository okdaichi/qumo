package ingest

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/msf"
)

const (
	defaultRingSize = 8

	notifyTimeout = 1 * time.Millisecond
)

var _ moqt.TrackHandler = (*ingestHandler)(nil)

// ---------------------------------------------------------------------------
// ingestHandler — moqt.TrackHandler implementation
// ---------------------------------------------------------------------------

// ingestHandler bridges a single publish stream to MoQT subscribers.
// It implements [moqt.TrackHandler]; the TrackMux calls ServeTrack once per
// subscribing client.
//
// Track routing and catalog management are delegated to an [msf.Broadcast].
// Video and audio track handlers are registered via [registerVideo] and
// [registerAudio] when codec configuration becomes available.
type ingestHandler struct {
	broadcast *msf.Broadcast
	video     *videoTrack
	audio     *singleTrack
	once      sync.Once
}

func newIngestHandler(ctx context.Context) (*ingestHandler, error) {
	b, err := msf.NewBroadcast(msf.Catalog{Version: 1})
	if err != nil {
		return nil, fmt.Errorf("initializing broadcast: %w", err)
	}
	metricPublishersActive.Inc()
	return &ingestHandler{
		broadcast: b,
		video:     newVideoTrack(ctx),
		audio:     newSingleTrack(ctx),
	}, nil
}

// ServeTrack is called by TrackMux for each subscribing MoQT client. It
// blocks until the subscriber disconnects or the publisher ends.
func (h *ingestHandler) ServeTrack(tw *moqt.TrackWriter) {
	h.broadcast.ServeTrack(tw)
}

// registerVideo adds (or replaces) the video track in the broadcast catalog
// and wires a handler that streams from the video [trackBuffer].
func (h *ingestHandler) registerVideo(cfg *AVCConfig) error {
	if cfg == nil {
		return fmt.Errorf("video config must not be nil")
	}
	track := msf.Track{
		Name:      "video",
		Packaging: msf.PackagingLOC,
		Role:      msf.RoleVideo,
		IsLive:    new(true),
		Codec:     cfg.CodecString(),
		Width:     new(int64(cfg.Width)),
		Height:    new(int64(cfg.Height)),
	}
	return h.broadcast.RegisterTrack(track, moqt.TrackHandlerFunc(func(tw *moqt.TrackWriter) {
		h.video.serve(tw)
	}))
}

// registerAudio adds (or replaces) the audio track in the broadcast catalog
// and wires a handler that streams from the audio [trackBuffer].
func (h *ingestHandler) registerAudio(cfg *AACConfig) error {
	if cfg == nil {
		return fmt.Errorf("audio config must not be nil")
	}
	track := msf.Track{
		Name:          "audio",
		Packaging:     msf.PackagingLOC,
		Role:          msf.RoleAudio,
		IsLive:        new(true),
		Codec:         cfg.CodecString(),
		SampleRate:    new(int64(cfg.SampleRate)),
		ChannelConfig: strconv.Itoa(cfg.ChannelConfig),
	}
	return h.broadcast.RegisterTrack(track, moqt.TrackHandlerFunc(func(tw *moqt.TrackWriter) {
		h.audio.serve(tw)
	}))
}

// close signals all subscribers that the publisher has disconnected.
func (h *ingestHandler) close() {
	h.once.Do(func() {
		h.video.close()
		metricPublishersActive.Dec()
	})
}

// ---------------------------------------------------------------------------
// videoTrack — keyframe-based group management
// ---------------------------------------------------------------------------

// videoTrack manages a video track with keyframe-based group boundaries.
// Each keyframe opens a new MoQT group so that subscribers joining
// mid-stream always get a clean decode point (GOP alignment).
type videoTrack struct {
	buf       *trackBuffer
	ctx       context.Context
	currentMu sync.Mutex
	current   *sourceGroup
}

func newVideoTrack(ctx context.Context) *videoTrack {
	return &videoTrack{buf: newTrackBuffer(ctx, "video"), ctx: ctx}
}

func (v *videoTrack) push(f *moqt.Frame, isKeyframe bool) {
	v.currentMu.Lock()
	if v.current == nil || isKeyframe {
		if v.current != nil {
			v.current.complete.Store(true)
		}
		v.current = v.buf.openGroup()
	}
	v.current.append(f)
	v.currentMu.Unlock()

	v.buf.notify()
}

// serve writes buffered groups to a single MoQT TrackWriter. It blocks
// until the subscriber disconnects or the publisher ends.
func (v *videoTrack) serve(tw *moqt.TrackWriter) {
	v.buf.serve(v.ctx, tw)
}

// close marks the current open group as complete. Called when the publisher
// disconnects.
func (v *videoTrack) close() {
	v.currentMu.Lock()
	if v.current != nil {
		v.current.complete.Store(true)
	}
	v.currentMu.Unlock()

	v.buf.notify()
}

// ---------------------------------------------------------------------------
// singleTrack — one frame per group
// ---------------------------------------------------------------------------

// singleTrack manages a track where each push creates an independent,
// single-frame, immediately-complete group. This is the right model for
// audio (each AAC frame is independently decodable and warrants its own
// QUIC stream) and for catalog updates.
type singleTrack struct {
	buf *trackBuffer
	ctx context.Context
}

func newSingleTrack(ctx context.Context) *singleTrack {
	return &singleTrack{buf: newTrackBuffer(ctx, "audio"), ctx: ctx}
}

func (s *singleTrack) push(f *moqt.Frame) {
	g := s.buf.openGroup()
	g.append(f)
	g.complete.Store(true)

	s.buf.notify()
}

// serve writes buffered groups to a single MoQT TrackWriter. It blocks
// until the subscriber disconnects or the publisher ends.
func (s *singleTrack) serve(tw *moqt.TrackWriter) {
	s.buf.serve(s.ctx, tw)
}

// ---------------------------------------------------------------------------
// trackBuffer — ring buffer + subscriber fan-out
// ---------------------------------------------------------------------------

// trackBuffer is the shared ring buffer with subscriber fan-out for a
// single MoQT track. Like [bytes.Buffer], it is a reusable data structure
// that decouples producers (push side) from consumers (serve side).
type trackBuffer struct {
	name string
	ctx  context.Context
	ring []atomic.Pointer[sourceGroup]
	size uint64
	pos  atomic.Uint64 // monotonically increasing; first group = 1

	subMu       sync.RWMutex
	subscribers map[chan struct{}]struct{}
}

func newTrackBuffer(ctx context.Context, name string) *trackBuffer {
	b := &trackBuffer{
		name:        name,
		ctx:         ctx,
		ring:        make([]atomic.Pointer[sourceGroup], defaultRingSize),
		size:        defaultRingSize,
		subscribers: make(map[chan struct{}]struct{}),
	}
	go b.pollCacheDepth()
	return b
}

func (b *trackBuffer) pollCacheDepth() {
	defer metricBufferDepthGroups.DeleteLabelValues(b.name)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			head := b.head()
			earliest := b.earliestAvailable()
			depth := 0
			if head >= earliest {
				depth = int(head - earliest + 1)
			}
			metricBufferDepthGroups.WithLabelValues(b.name).Set(float64(depth))
		}
	}
}

func (b *trackBuffer) openGroup() *sourceGroup {
	p := b.pos.Add(1)
	g := &sourceGroup{
		seq:    moqt.GroupSequence(p),
		frames: make([]*moqt.Frame, 0, 4),
	}
	b.ring[p%b.size].Store(g)
	return g
}

// --- subscriber notification ---

func (b *trackBuffer) notify() {
	b.subMu.RLock()
	for ch := range b.subscribers {
		if len(ch) == 0 {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	b.subMu.RUnlock()
}

func (b *trackBuffer) subscribe() chan struct{} {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	ch := make(chan struct{}, 1)
	b.subscribers[ch] = struct{}{}
	return ch
}

func (b *trackBuffer) unsubscribe(ch chan struct{}) {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	delete(b.subscribers, ch)
}

// --- ring buffer access ---

func (b *trackBuffer) head() moqt.GroupSequence {
	return moqt.GroupSequence(b.pos.Load())
}

func (b *trackBuffer) earliestAvailable() moqt.GroupSequence {
	h := b.head()
	if h <= moqt.GroupSequence(b.size) {
		return 1
	}
	return h - moqt.GroupSequence(b.size) + 1
}

func (b *trackBuffer) get(seq moqt.GroupSequence) *sourceGroup {
	return b.ring[uint64(seq)%b.size].Load()
}

// --- subscriber egress ---

// serve writes groups and frames to a single MoQT TrackWriter. It blocks
// until the subscriber disconnects or ctx is cancelled.
func (b *trackBuffer) serve(ctx context.Context, tw *moqt.TrackWriter) {
	twCtx := tw.Context()

	metricSubscribersActive.Inc()
	defer metricSubscribersActive.Dec()

	notify := b.subscribe()
	defer b.unsubscribe(notify)

	last := b.head()
	if last > 0 {
		last--
	}

	timer := time.NewTimer(notifyTimeout)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	for {
		latest := b.head()

		if last < latest {
			last++

			earliest := b.earliestAvailable()
			if last < earliest {
				// Subscriber fell behind; skip to latest.
				metricSubscriberSkipsTotal.Inc()
				last = latest - 1
				continue
			}

			g := b.get(last)
			if g == nil {
				last--
				continue
			}

			gw, err := tw.OpenGroupAt(ctx, g.seq)
			if err != nil {
				metricSubscribeErrorsTotal.WithLabelValues("open_group_failed").Inc()
				return
			}
			start := time.Now()

			frameIdx := 0
			for {
				f := g.next(frameIdx)
				if f != nil {
					if err := gw.WriteFrame(f); err != nil {
						_ = gw.Close()
						return
					}
					frameIdx++
					continue
				}

				if g.isComplete() {
					break
				}

				// Wait for more frames within this group.
				timer.Reset(notifyTimeout)
				select {
				case <-notify:
				case <-timer.C:
				case <-ctx.Done():
					_ = gw.Close()
					return
				case <-twCtx.Done():
					_ = gw.Close()
					return
				}
			}

			_ = gw.Close()
			metricGroupDeliveryHistogram.WithLabelValues(b.name).Observe(time.Since(start).Seconds())
			continue
		}

		// No new groups yet; wait for data.
		timer.Reset(notifyTimeout)
		select {
		case <-notify:
		case <-timer.C:
		case <-ctx.Done():
			return
		case <-twCtx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// sourceGroup
// ---------------------------------------------------------------------------

// sourceGroup holds the frames of a single MoQT group.
type sourceGroup struct {
	seq      moqt.GroupSequence
	mu       sync.Mutex
	frames   []*moqt.Frame
	complete atomic.Bool
}

func (g *sourceGroup) append(f *moqt.Frame) {
	g.mu.Lock()
	g.frames = append(g.frames, f)
	g.mu.Unlock()
}

func (g *sourceGroup) next(index int) *moqt.Frame {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index >= len(g.frames) {
		return nil
	}
	return g.frames[index]
}

func (g *sourceGroup) isComplete() bool {
	return g.complete.Load()
}
