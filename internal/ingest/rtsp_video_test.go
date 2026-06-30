package ingest

import (
	"encoding/base64"
	"testing"

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
// without a session (pushNALU returns early on nil session). This proves the
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

