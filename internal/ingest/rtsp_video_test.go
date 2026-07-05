package ingest

import (
	"encoding/base64"
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/rtsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractParameterSets verifies the H.264 sprop-parameter-sets fmtp
// parser splits base64-encoded NAL units into SPS (type 7) and PPS (type 8).
func TestExtractParameterSets(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9} // NAL type 7
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB}             // NAL type 8

	t.Run("sps and pps", func(t *testing.T) {
		fmtp := "packetization-mode=1; sprop-parameter-sets=" +
			base64.StdEncoding.EncodeToString(sps) + "," +
			base64.StdEncoding.EncodeToString(pps) +
			"; profile-level-id=42e01e"

		gotSPS, gotPPS := extractParameterSets(fmtp)
		require.Len(t, gotSPS, 1)
		require.Len(t, gotPPS, 1)
		assert.Equal(t, sps, gotSPS[0])
		assert.Equal(t, pps, gotPPS[0])
	})

	t.Run("multiple sps/pps in any order", func(t *testing.T) {
		sps2 := []byte{0x67, 0x4D}
		fmtp := "sprop-parameter-sets=" +
			base64.StdEncoding.EncodeToString(sps) + "," +
			base64.StdEncoding.EncodeToString(pps) + "," +
			base64.StdEncoding.EncodeToString(sps2)

		gotSPS, gotPPS := extractParameterSets(fmtp)
		assert.Len(t, gotSPS, 2)
		assert.Len(t, gotPPS, 1)
	})

	t.Run("missing key returns nil", func(t *testing.T) {
		gotSPS, gotPPS := extractParameterSets("packetization-mode=1; profile-level-id=42e01e")
		assert.Nil(t, gotSPS)
		assert.Nil(t, gotPPS)
	})

	t.Run("malformed base64 entries are skipped", func(t *testing.T) {
		// "!!!not-base64!!!" decodes with an error and is skipped; the valid
		// SPS alongside it still comes through.
		fmtp := "sprop-parameter-sets=!!!not-base64!!!," +
			base64.StdEncoding.EncodeToString(sps)
		gotSPS, gotPPS := extractParameterSets(fmtp)
		require.Len(t, gotSPS, 1)
		assert.Equal(t, sps, gotSPS[0])
		assert.Nil(t, gotPPS)
	})
}

// fuIndicator builds an FU-A indicator (NAL type 28) carrying the given NRI.
func fuIndicator(nri uint8) byte {
	return (nri << 5) | 28 // forbidden_zero=0 | NRI | type=28
}

// TestReassembleFU covers H.264 FU-A fragmentation reassembly: the completed
// NAL unit's first byte is (indicator & 0xE0) | type, and the payloads of all
// fragments concatenate in order.
func TestReassembleFU(t *testing.T) {
	t.Run("split across start and end", func(t *testing.T) {
		const nalType uint8 = 5 // IDR
		track := &rtspTrack{}

		// Start fragment: FU header 0x80|type, carries first payload byte.
		start := []byte{fuIndicator(3), 0x80 | nalType, 0xAA}
		// End fragment: FU header 0x40|type, carries remaining bytes.
		end := []byte{fuIndicator(3), 0x40 | nalType, 0xBB, 0xCC}

		assert.Nil(t, track.reassembleFU(start), "start fragment must not complete a NAL")
		got := track.reassembleFU(end)
		require.NotNil(t, got)

		// Reconstructed first byte = indicator(0x7C)&0xE0 | nalType(5) = 0x65.
		assert.Equal(t, []byte{0x65, 0xAA, 0xBB, 0xCC}, got)
		assert.Nil(t, track.fuBuffer, "fuBuffer must reset after completion")
	})

	t.Run("single fragment with start and end", func(t *testing.T) {
		const nalType uint8 = 1
		track := &rtspTrack{}
		// FU header 0xC0|type = start+end in one packet.
		pkt := []byte{fuIndicator(2), 0xC0 | nalType, 0xDD, 0xEE}
		got := track.reassembleFU(pkt)
		require.NotNil(t, got)
		assert.Equal(t, []byte{(fuIndicator(2) & 0xE0) | nalType, 0xDD, 0xEE}, got)
	})

	t.Run("continuation fragments append without resetting", func(t *testing.T) {
		const nalType uint8 = 5
		track := &rtspTrack{}
		track.reassembleFU([]byte{fuIndicator(3), 0x80 | nalType, 0x01}) // start
		// Middle fragment: neither start nor end (0x00|type).
		assert.Nil(t, track.reassembleFU([]byte{fuIndicator(3), nalType, 0x02, 0x03}))
		got := track.reassembleFU([]byte{fuIndicator(3), 0x40 | nalType, 0x04})
		assert.Equal(t, []byte{0x65, 0x01, 0x02, 0x03, 0x04}, got)
	})

	t.Run("a new start discards any in-flight fragment", func(t *testing.T) {
		const nalType uint8 = 5
		track := &rtspTrack{}
		track.reassembleFU([]byte{fuIndicator(3), 0x80 | nalType, 0x01}) // start, no end
		// A second start before the first completed resets reassembly.
		got := track.reassembleFU([]byte{fuIndicator(3), 0x80 | nalType, 0x09, 0x0A})
		assert.Nil(t, got)
		got = track.reassembleFU([]byte{fuIndicator(3), 0x40 | nalType, 0x0B})
		assert.Equal(t, []byte{0x65, 0x09, 0x0A, 0x0B}, got)
	})

	t.Run("short payload is ignored", func(t *testing.T) {
		track := &rtspTrack{}
		assert.Nil(t, track.reassembleFU([]byte{fuIndicator(3)})) // only indicator, no FU header
	})

	t.Run("middle/end fragment without an active start is dropped", func(t *testing.T) {
		track := &rtspTrack{}
		const nalType uint8 = 5
		// End fragment with no preceding start: must not build a malformed,
		// headerless NAL unit.
		got := track.reassembleFU([]byte{fuIndicator(3), 0x40 | nalType, 0xAA, 0xBB})
		assert.Nil(t, got)
		assert.Nil(t, track.fuBuffer, "no reassembly should have started")
	})

	t.Run("buffer cap discards an over-large NAL and resets", func(t *testing.T) {
		const nalType uint8 = 5
		track := &rtspTrack{}

		// Shrink the cap so the test does not allocate 16 MiB.
		old := maxFUBufferSize
		maxFUBufferSize = 8
		t.Cleanup(func() { maxFUBufferSize = old })

		// Start, then a continuation that blows past the 8-byte cap.
		track.reassembleFU([]byte{fuIndicator(3), 0x80 | nalType, 0x01}) // start
		assert.Nil(t, track.reassembleFU([]byte{fuIndicator(3), nalType, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}))
		assert.Nil(t, track.fuBuffer, "over-large reassembly must reset fuBuffer")

		// A subsequent middle fragment (no start) must be dropped while idle.
		assert.Nil(t, track.reassembleFU([]byte{fuIndicator(3), nalType, 0x0A}))
	})
}

// TestHandleVideoRTP_Paths exercises the single-NAL and FU-A routing branches
// without a session (flushAccessUnit is a no-op on nil session). This proves the
// dispatch and reassembly glue do not panic; the reassembly math itself is
// asserted directly in TestReassembleFU.
func TestHandleVideoRTP_Paths(t *testing.T) {
	t.Run("single NAL does not panic", func(t *testing.T) {
		track := &rtspTrack{}
		track.handleVideoRTP(&rtsp.RTPPacket{
			Header:  rtsp.RTPHeader{Timestamp: 90000},
			Payload: []byte{0x65, 0xAA, 0xBB}, // single IDR NAL
		})
	})

	t.Run("FU-A reassembly does not panic", func(t *testing.T) {
		track := &rtspTrack{}
		const nalType uint8 = 5
		track.handleVideoRTP(&rtsp.RTPPacket{Payload: []byte{fuIndicator(3), 0x80 | nalType, 0xAA}})
		track.handleVideoRTP(&rtsp.RTPPacket{Payload: []byte{fuIndicator(3), 0x40 | nalType, 0xBB}})
	})
}

// TestParseSTAPA verifies STAP-A (RFC 6184 §5.7) aggregation splitting: each
// component NALU is preceded by a 2-byte big-endian length. RTSP ingest must
// split STAP-A so the component NALUs feed access-unit aggregation; before this
// was handled the whole packet was silently dropped.
func TestParseSTAPA(t *testing.T) {
	// STAP-A indicator (type 24) + [len=3][A B C][len=1][D].
	stapA := []byte{0x78, 0x00, 0x03, 0xAA, 0xBB, 0xCC, 0x00, 0x01, 0xDD}
	got := parseSTAPA(stapA)
	require.Len(t, got, 2)
	assert.Equal(t, []byte{0xAA, 0xBB, 0xCC}, got[0])
	assert.Equal(t, []byte{0xDD}, got[1])

	t.Run("truncated length field stops parsing", func(t *testing.T) {
		// Only the indicator + one byte (incomplete length).
		assert.Empty(t, parseSTAPA([]byte{0x78, 0x00}))
	})

	t.Run("truncated NALU stops parsing", func(t *testing.T) {
		// Claims 5 bytes but only 2 follow.
		assert.Empty(t, parseSTAPA([]byte{0x78, 0x00, 0x05, 0xAA, 0xBB}))
	})

	t.Run("zero-length NALU stops parsing", func(t *testing.T) {
		// A zero-length entry must not produce an empty NALU (avoids a
		// headerless entry confusing downstream AVCC framing).
		assert.Empty(t, parseSTAPA([]byte{0x78, 0x00, 0x00, 0x00, 0x01, 0xAA}))
	})
}

// TestAppendAVCC verifies AVCC framing for a multi-NALU access unit: each NALU
// gets a 4-byte big-endian length prefix, concatenated. This is the sample
// format the WebCodecs VideoDecoder consumes and what RTMP ingest emits too.
func TestAppendAVCC(t *testing.T) {
	got := appendAVCC(nil, []byte{0x65, 0x01})
	got = appendAVCC(got, []byte{0x01, 0x02, 0x03})

	assert.Equal(t, []byte{
		0x00, 0x00, 0x00, 0x02, 0x65, 0x01, // [len=2][IDR…]
		0x00, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03, // [len=3][non-IDR slice]
	}, got)
}

// readQuicVarint decodes a QUIC varint from the start of b, returning the
// value and the number of bytes consumed. Test-only inverse of putQuicVarint.
func readQuicVarint(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	prefix := b[0] >> 6
	length := 1 << prefix
	v := uint64(b[0] & 0x3F)
	for i := 1; i < length; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, length
}

// decodeMediaFrameBody extracts the codec-specific body and timestamp from a
// MediaFrame envelope produced by writeMediaFrame. Test helper.
func decodeMediaFrameBody(b []byte) (tsUS int64, body []byte) {
	ts, n := readQuicVarint(b)
	dataLen, m := readQuicVarint(b[n:])
	return int64(ts), b[n+m:][:dataLen]
}

// firstVideoFrameBody returns the decoded AVCC body and timestamp of frame
// index `i` in the video track's current group. Test helper for asserting what
// flushAccessUnit pushed.
func videoFrameBody(t *testing.T, sess *Session, idx int) (int64, []byte) {
	t.Helper()
	g := sess.handler.video.buf.get(sess.handler.video.buf.head())
	require.NotNil(t, g)
	fr := g.next(idx)
	require.NotNil(t, fr)
	return decodeMediaFrameBody(fr.Body())
}

// newAggregationSession builds a fresh ingest Session + rtspTrack wired to a
// registered AVC track, for isolated access-unit aggregation subtests.
func newAggregationSession(t *testing.T, path string) (*Session, *rtspTrack) {
	t.Helper()
	sess, err := NewSession(moqt.NewTrackMux(0), moqt.BroadcastPath(path))
	require.NoError(t, err)
	registerTestVideo(t, sess)
	return sess, &rtspTrack{session: sess}
}

// TestHandleVideoRTP_AccessUnitAggregation is the unit-level guard for the
// "broken picture" defect: NALUs sharing an RTP timestamp (one access unit) must
// be aggregated into a single AVCC sample pushed as one frame, and the keyframe
// flag must be set when the AU contains an IDR slice. ffmpeg's RTSP muxer emits
// several same-timestamp IDR NALUs per keyframe; before the fix each was pushed
// as its own frame, so only the first reached the WebCodecs decoder as `key`.
func TestHandleVideoRTP_AccessUnitAggregation(t *testing.T) {
	t.Run("same-timestamp NALUs aggregate into one AVCC sample", func(t *testing.T) {
		sess, track := newAggregationSession(t, "/live/au-aggregate")
		defer sess.Close()

		// Three IDR slices (NAL type 5) at the same RTP timestamp — exactly what
		// ffmpeg's RTSP muxer emits per keyframe. Marker bit set on the last.
		const ts uint32 = 90000
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: ts}, Payload: []byte{0x65, 0xA1}})
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: ts}, Payload: []byte{0x65, 0xA2}})
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: ts, Marker: true}, Payload: []byte{0x65, 0xA3}})

		// One group, one frame — not three.
		g := sess.handler.video.buf.get(sess.handler.video.buf.head())
		require.NotNil(t, g)
		require.Len(t, g.frames, 1, "same-timestamp NALUs must aggregate into one frame")

		gotTS, body := videoFrameBody(t, sess, 0)
		assert.Equal(t, int64(ts)*1000/90, gotTS, "PTS = RTP ts (90kHz) -> microseconds")
		want := appendAVCC(appendAVCC(appendAVCC(nil, []byte{0x65, 0xA1}), []byte{0x65, 0xA2}), []byte{0x65, 0xA3})
		assert.Equal(t, want, body)
	})

	t.Run("timestamp change flushes the in-flight access unit", func(t *testing.T) {
		sess, track := newAggregationSession(t, "/live/au-flush")
		defer sess.Close()

		// Open a keyframe group so subsequent non-IDR AUs land in a known group.
		track.handleVideoRTP(&rtsp.RTPPacket{
			Header:  rtsp.RTPHeader{Timestamp: 90000, Marker: true},
			Payload: []byte{0x65, 0x00}, // IDR
		})
		group := sess.handler.video.buf.get(sess.handler.video.buf.head())
		require.NotNil(t, group)
		require.Len(t, group.frames, 1)

		// AU0: non-IDR, no marker — held in the accumulator.
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: 180000}, Payload: []byte{0x41, 0xB0}})
		require.Len(t, group.frames, 1, "non-marker non-boundary packet must not flush yet")

		// AU1 at a new timestamp flushes AU0 first, then AU1 is flushed by its marker.
		track.handleVideoRTP(&rtsp.RTPPacket{
			Header:  rtsp.RTPHeader{Timestamp: 270000, Marker: true},
			Payload: []byte{0x41, 0xC0},
		})
		require.Len(t, group.frames, 3, "timestamp change + marker must flush AU0 then AU1")
	})

	t.Run("IDR access unit opens a new group (keyframe), non-IDR does not", func(t *testing.T) {
		sess, track := newAggregationSession(t, "/live/au-keyflag")
		defer sess.Close()

		// First IDR opens group 1.
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: 90000, Marker: true}, Payload: []byte{0x65, 0x01}})
		head1 := sess.handler.video.buf.head()

		// Non-IDR AUs append to the same group; head does not advance.
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: 180000, Marker: true}, Payload: []byte{0x41, 0x02}})
		assert.Equal(t, head1, sess.handler.video.buf.head(), "non-IDR AU must not open a new group")

		// A later IDR opens a fresh group.
		track.handleVideoRTP(&rtsp.RTPPacket{Header: rtsp.RTPHeader{Timestamp: 270000, Marker: true}, Payload: []byte{0x65, 0x03}})
		assert.Greater(t, sess.handler.video.buf.head(), head1, "IDR AU must open a new group (keyframe)")
	})
}

// registerTestVideo registers a minimal AVC track so PushVideo has somewhere to
// route. The SPS/PPS contents are irrelevant to the aggregation assertions.
func registerTestVideo(t *testing.T, sess *Session) {
	t.Helper()
	cfg := &AVCConfig{
		ProfileIDC: 0x64, ProfileCompat: 0x00, LevelIDC: 0x1F,
		NALULenSize: 4,
		SPS:         [][]byte{{0x67, 0x64, 0x00, 0x1F, 0xAC}},
		PPS:         [][]byte{{0x68, 0xEB, 0xE3, 0xCB}},
	}
	require.NoError(t, sess.RegisterVideo(cfg))
}

// TestWrapAVCC verifies the AVCC length-prefix framing RTSP pushes for each
// NAL unit — it must be a 4-byte big-endian length followed by the NALU, the
// sample-stream format matching the avc1 codec string + initData.
func TestWrapAVCC(t *testing.T) {
	nalu := []byte{0x65, 0xDE, 0xAD, 0xBE, 0xEF}
	got := wrapAVCC(nalu)

	require.Len(t, got, 4+len(nalu))
	// 4-byte big-endian length prefix.
	assert.Equal(t, []byte{0x00, 0x00, 0x00, byte(len(nalu))}, got[:4])
	assert.Equal(t, nalu, got[4:])
}

// TestParseInterleavedChannels verifies the RTSP SETUP Transport-header
// parser. ffmpeg sends parameters in varying order (e.g.
// "RTP/AVP/TCP;unicast;interleaved=0-1"); the old strict Sscanf rejected it
// with 400 Bad Request, aborting every ffmpeg RTSP publish.
func TestParseInterleavedChannels(t *testing.T) {
	tests := map[string]struct {
		transport string
		rtp, rtcp uint8
		ok        bool
	}{
		"ffmpeg TCP interleaved pair": {"RTP/AVP/TCP;unicast;interleaved=0-1", 0, 1, true},
		"with mode=RECORD":            {"RTP/AVP/TCP;unicast;mode=RECORD;interleaved=5-6", 5, 6, true},
		"simple pair":                 {"RTP/AVP/TCP;interleaved=2-3", 2, 3, true},
		"single channel maps to both": {"RTP/AVP/TCP;interleaved=4", 4, 4, true},
		"no interleaved token":        {"RTP/AVP;unicast;client_port=5000-5001", 0, 0, false},
		"malformed value":             {"RTP/AVP/TCP;interleaved=foo", 0, 0, false},
		"channel > 255 rejected":      {"RTP/AVP/TCP;interleaved=300-301", 0, 0, false},
		"negative channel rejected":   {"RTP/AVP/TCP;interleaved=-1", 0, 0, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rtp, rtcp, ok := parseInterleavedChannels(tt.transport)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.rtp, rtp)
				assert.Equal(t, tt.rtcp, rtcp)
			}
		})
	}
}
