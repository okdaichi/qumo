package ingest

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
			transport := req.Header.Get("Transport")
			if !strings.Contains(transport, "interleaved") {
				resp.StatusCode = rtsp.StatusUnsupportedTransport
				break
			}
			// Parse interleaved channels
			var rtpChan, rtcpChan uint8
			fmt.Sscanf(transport, "RTP/AVP/TCP;interleaved=%d-%d", &rtpChan, &rtcpChan)
			
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
						if idx := strings.Index(fmtp, "sprop-parameter-sets="); idx != -1 {
							sets := strings.Split(strings.Split(fmtp[idx+21:], ";")[0], ",")
							var sps, pps [][]byte
							for _, s := range sets {
								b, _ := base64.StdEncoding.DecodeString(s)
								if len(b) > 0 {
									if (b[0] & 0x1F) == 7 {
										sps = append(sps, b)
									} else if (b[0] & 0x1F) == 8 {
										pps = append(pps, b)
									}
								}
							}
							if len(sps) > 0 {
								cfg := &AVCConfig{
									ProfileIDC:    sps[0][1],
									ProfileCompat: sps[0][2],
									LevelIDC:      sps[0][3],
									SPS:           sps,
									PPS:           pps,
									// Width/Height should be parsed from SPS, but using placeholder for now
									Width:  1920,
									Height: 1080,
								}
								sess.RegisterVideo(cfg)
								track.avcCfg = cfg
							}
						}
					}
				} else if media.Type == "audio" && strings.Contains(media.RtpMap, "mpeg4-generic") {
					track.kind = trackKindAudio
					if fmtp := media.Fmtp; fmtp != "" {
						if idx := strings.Index(fmtp, "config="); idx != -1 {
							configHex := strings.Split(fmtp[idx+7:], ";")[0]
							config, _ := hex.DecodeString(configHex)
							if len(config) >= 2 {
								// Simple AAC config parsing (placeholder)
								cfg := &AACConfig{
									SampleRate:    44100,
									ChannelConfig: 2,
								}
								sess.RegisterAudio(cfg)
							}
						}
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
		// Audio handling (placeholder)
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
		// FU-A
		if len(p.Payload) < 2 {
			return
		}
		fuHeader := p.Payload[1]
		start := (fuHeader >> 7) & 1
		end := (fuHeader >> 6) & 1
		nalType := fuHeader & 0x1F

		if start == 1 {
			t.fuBuffer = []byte{(p.Payload[0] & 0xE0) | nalType}
		}
		t.fuBuffer = append(t.fuBuffer, p.Payload[2:]...)

		if end == 1 {
			t.pushNALU(p.Header.Timestamp, t.fuBuffer)
			t.fuBuffer = nil
		}
	}
}

func (t *rtspTrack) pushNALU(timestamp uint32, nalu []byte) {
	if t.session == nil {
		return
	}
	// Convert to Annex-B (prefixed with start code)
	data := append([]byte{0, 0, 0, 1}, nalu...)
	pts := int64(timestamp) * 1000 / 90 // 90kHz clock to microseconds
	isKey := (nalu[0] & 0x1F) == 5
	t.session.PushVideo(pts, data, isKey)
}
