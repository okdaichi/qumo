package ingest

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/rtsp"
)

// RTSPConfig holds configuration for an [RTSPServer].
type RTSPConfig struct {
	// Addr is the TCP address to listen on (e.g., ":554").
	Addr string

	// TrackMux is the MoQT multiplexer where ingested streams are announced.
	TrackMux *moqt.TrackMux
}

// RTSPServer accepts RTSP push connections and bridges each stream to
// MoQT via an ingest [Session].
type RTSPServer struct {
	config RTSPConfig

	mu       sync.Mutex
	listener *rtsp.Listener

	connWg     sync.WaitGroup
	connCancel context.CancelFunc
}

// NewRTSPServer creates a new RTSP ingest server with the given config.
func NewRTSPServer(cfg RTSPConfig) *RTSPServer {
	return &RTSPServer{config: cfg}
}

// ListenAndServe starts the RTSP listener and blocks until ctx is
// cancelled or an unrecoverable error occurs.
func (s *RTSPServer) ListenAndServe(ctx context.Context) error {
	ln, err := rtsp.Listen("tcp", s.config.Addr)
	if err != nil {
		return err
	}

	connCtx, connCancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.listener = ln
	s.connCancel = connCancel
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		s.connWg.Add(1)
		go func() {
			defer s.connWg.Done()
			s.handleConn(connCtx, conn)
		}()
	}
}

// Shutdown gracefully stops the RTSP listener.
func (s *RTSPServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}

	done := make(chan struct{})
	go func() {
		s.connWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *RTSPServer) handleConn(ctx context.Context, conn *rtsp.Conn) {
	defer conn.Close()

	var (
		sess   *Session
		tracks = make(map[uint8]*rtspTrack) // channel -> track
		sdp    *rtsp.SDP
	)

	for {
		req, frame, err := conn.ReadRequest()
		if err != nil {
			if err != io.EOF {
				slog.Warn("RTSP read error", "error", err)
			}
			return
		}

		if frame != nil {
			if track, ok := tracks[frame.Channel]; ok {
				track.handleFrame(frame)
			}
			continue
		}

		// Handle RTSP Request
		resp := rtsp.NewResponse(rtsp.StatusOK, req)
		if cseq := req.Header.Get("CSeq"); cseq != "" {
			resp.Header.Set("CSeq", cseq)
		}

		switch req.Method {
		case rtsp.MethodOptions:
			resp.Header.Set("Public", "DESCRIBE, SETUP, TEARDOWN, PLAY, PAUSE, ANNOUNCE, RECORD")

		case rtsp.MethodAnnounce:
			if req.Body == nil {
				resp.StatusCode = rtsp.StatusBadRequest
				break
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				resp.StatusCode = rtsp.StatusBadRequest
				break
			}
			sdp = rtsp.ParseSDP(string(body))
			path := moqt.BroadcastPath(req.URL.Path)
			if path == "" {
				path = "/live/stream"
			}
			sess, err = NewSession(s.config.TrackMux, path)
			if err != nil {
				slog.Error("failed to create ingest session", "error", err)
				resp.StatusCode = rtsp.StatusInternalServerError
				break
			}
			defer sess.Close()

		case rtsp.MethodSetup:
			if sdp == nil || sess == nil {
				// SETUP before ANNOUNCE, or ANNOUNCE failed.
				resp.StatusCode = rtsp.StatusBadRequest
				break
			}
			transport := req.Header.Get("Transport")
			if !strings.Contains(transport, "interleaved") {
				resp.StatusCode = rtsp.StatusUnsupportedTransport
				break
			}
			// Parse interleaved channels.
			var rtpChan, rtcpChan uint8
			if _, err := fmt.Sscanf(transport, "RTP/AVP/TCP;interleaved=%d-%d", &rtpChan, &rtcpChan); err != nil {
				resp.StatusCode = rtsp.StatusBadRequest
				break
			}

			// Find corresponding media in SDP
			var media *rtsp.SDPMedia
			for i := range sdp.Medias {
				m := &sdp.Medias[i]
				if strings.Contains(req.URL.String(), m.Control) || m.Control == "*" {
					media = m
					break
				}
			}

			track := &rtspTrack{
				session: sess,
			}
			if media != nil {
				if media.Type == "video" && strings.Contains(media.RtpMap, "H264") {
					track.kind = trackKindVideo
					// Extract SPS/PPS
					if fmtp := media.Fmtp; fmtp != "" {
						sps, pps := extractParameterSets(fmtp)
						if len(sps) > 0 && len(sps[0]) >= 4 {
							cfg := &AVCConfig{
								ProfileIDC:    sps[0][1],
								ProfileCompat: sps[0][2],
								LevelIDC:      sps[0][3],
								NALULenSize:   4,
								SPS:           sps,
								PPS:           pps,
								// Width/Height should be parsed from SPS, but using placeholder for now
								Width:  1920,
								Height: 1080,
							}
							if err := sess.RegisterVideo(cfg); err != nil {
								slog.Warn("failed to register video track", "error", err)
							}
							track.avcCfg = cfg
						}
					}
				} else if media.Type == "audio" && strings.Contains(media.RtpMap, "mpeg4-generic") {
					track.kind = trackKindAudio
					cfg := parseAACConfigFromFmtp(media.Fmtp)
					track.aacDepack = newAACDepacketizer(media.Fmtp, cfg.SampleRate)
					if err := sess.RegisterAudio(cfg); err != nil {
						slog.Warn("failed to register audio track", "error", err)
					}
				}
			}

			tracks[rtpChan] = track
			resp.Header.Set("Transport", transport)
			resp.Header.Set("Session", "12345678")

		case rtsp.MethodRecord:
			slog.Info("RTSP recording started", "remote", conn.RemoteAddr())

		case rtsp.MethodTeardown:
			_ = conn.WriteResponse(resp)
			return

		default:
			// Unsupported method (e.g. DESCRIBE, PLAY) for a push-only ingest.
			resp.StatusCode = rtsp.StatusMethodNotAllowed
		}

		if err := conn.WriteResponse(resp); err != nil {
			return
		}
	}
}

type trackKind int

const (
	trackKindVideo trackKind = iota
	trackKindAudio
)

type rtspTrack struct {
	session *Session
	kind    trackKind
	avcCfg  *AVCConfig

	// aacDepack reassembles AAC access units from mpeg4-generic RTP payloads.
	aacDepack *aacDepacketizer

	// RTP reassembly
	fuBuffer []byte
}

func (t *rtspTrack) handleFrame(f *rtsp.InterleavedFrame) {
	packet, err := rtsp.UnmarshalRTP(f.Payload)
	if err != nil {
		return
	}

	if t.kind == trackKindVideo {
		t.handleVideoRTP(packet)
	} else {
		t.handleAudioRTP(packet)
	}
}

func (t *rtspTrack) handleAudioRTP(p *rtsp.RTPPacket) {
	if t.session == nil || t.aacDepack == nil {
		return
	}
	aus, err := t.aacDepack.depacketize(p.Payload, p.Header.Timestamp)
	if err != nil {
		return
	}
	for _, au := range aus {
		t.session.PushAudio(au.pts, au.data)
	}
}

func (t *rtspTrack) handleVideoRTP(p *rtsp.RTPPacket) {
	if len(p.Payload) == 0 {
		return
	}

	typ := p.Payload[0] & 0x1F
	switch {
	case typ >= 1 && typ <= 23:
		// Single NAL unit
		t.pushNALU(p.Header.Timestamp, p.Payload)
	case typ == 28:
		// FU-A fragmentation: reassemble across packets, push on the final fragment.
		if nalu := t.reassembleFU(p.Payload); nalu != nil {
			t.pushNALU(p.Header.Timestamp, nalu)
		}
	}
}

// maxFUBufferSize caps a single reassembled H.264 NAL unit. A real NAL is far
// smaller (even a 4K intra frame is a few MB), so this purely bounds memory
// against a malformed or hostile RTSP publisher that streams FU-A continuation
// fragments without ever setting the end bit. A var (not const) so tests can
// shrink it.
var maxFUBufferSize = 16 << 20 // 16 MiB

// reassembleFU handles H.264 FU-A (RFC 6184 §5.8) fragmentation. Each
// fragment's payload is appended to fuBuffer; the completed NAL unit is
// returned when the final (end) fragment arrives, and nil while a NAL unit is
// still in flight. The reconstructed first byte combines the FU indicator's
// forbidden-zero-bit + NRI (top 3 bits, 0xE0) with the FU header's NAL type
// (low 5 bits). Callers push the returned NAL unit via pushNALU.
//
// Safety: a middle/end fragment with no active reassembly (no preceding start)
// is dropped rather than appended to a nil buffer, which would yield a NAL
// missing its reconstructed header byte. If fuBuffer exceeds maxFUBufferSize
// the in-flight NAL is discarded and reassembly resets, bounding memory.
func (t *rtspTrack) reassembleFU(payload []byte) []byte {
	if len(payload) < 2 {
		return nil
	}
	fuHeader := payload[1]
	start := (fuHeader >> 7) & 1
	end := (fuHeader >> 6) & 1
	nalType := fuHeader & 0x1F

	if start == 1 {
		t.fuBuffer = []byte{(payload[0] & 0xE0) | nalType}
	} else if t.fuBuffer == nil {
		// Middle/end fragment without an active reassembly: drop it instead of
		// building a headerless (malformed) NAL unit.
		return nil
	}
	t.fuBuffer = append(t.fuBuffer, payload[2:]...)

	if len(t.fuBuffer) > maxFUBufferSize {
		// Exceeds the cap: abandon this NAL and reset, bounding memory.
		t.fuBuffer = nil
		return nil
	}

	if end == 1 {
		complete := t.fuBuffer
		t.fuBuffer = nil
		return complete
	}
	return nil
}

// wrapAVCC prefixes a NAL unit with a 4-byte big-endian length — the AVCC
// sample-stream format that matches the avc1 codec string and the
// AVCDecoderConfigurationRecord carried in the catalog initData.
func wrapAVCC(nalu []byte) []byte {
	data := make([]byte, 4+len(nalu))
	binary.BigEndian.PutUint32(data, uint32(len(nalu)))
	copy(data[4:], nalu)
	return data
}

func (t *rtspTrack) pushNALU(timestamp uint32, nalu []byte) {
	if t.session == nil {
		return
	}
	// Emit AVCC (length-prefixed), matching the avc1 catalog codec string.
	data := wrapAVCC(nalu)
	pts := int64(timestamp) * 1000 / 90 // 90kHz clock to microseconds
	isKey := (nalu[0] & 0x1F) == 5
	t.session.PushVideo(pts, data, isKey)
}

func extractParameterSets(fmtp string) (sps, pps [][]byte) {
	idx := strings.Index(fmtp, "sprop-parameter-sets=")
	if idx == -1 {
		return nil, nil
	}
	sets := strings.Split(strings.Split(fmtp[idx+21:], ";")[0], ",")
	for _, s := range sets {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(b) == 0 {
			continue
		}
		switch b[0] & 0x1F {
		case 7:
			sps = append(sps, b)
		case 8:
			pps = append(pps, b)
		}
	}
	return sps, pps
}
