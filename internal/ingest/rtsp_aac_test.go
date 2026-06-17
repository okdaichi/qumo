package ingest

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fmtpAAC48k is the mpeg4-generic fmtp string ffmpeg advertises for a 48 kHz
// stereo AAC-LC stream (-c:a aac -ar 48000 -ac 2): config=1190.
const fmtpAAC48k = "streamtype=5; profile-level-id=1; mode=AAC-hbr; " +
	"sizelength=13; indexlength=3; indexdeltalength=3; config=1190"

// TestParseAACConfigFromFmtp confirms the RTSP SETUP handler reads the real
// AAC config from the fmtp "config=" parameter instead of the previous
// hardcoded 44.1 kHz / 2 ch placeholder.
func TestParseAACConfigFromFmtp(t *testing.T) {
	t.Run("ffmpeg 48kHz stereo", func(t *testing.T) {
		cfg := parseAACConfigFromFmtp(fmtpAAC48k)
		require.NotNil(t, cfg)
		assert.Equal(t, byte(2), cfg.ObjectType, "AAC-LC object type")
		assert.Equal(t, 48000, cfg.SampleRate, "sample rate must come from the ASC, not be hardcoded")
		assert.Equal(t, 2, cfg.ChannelConfig)
	})

	t.Run("missing config falls back to a usable default", func(t *testing.T) {
		cfg := parseAACConfigFromFmtp("mode=AAC-hbr; sizelength=13; indexlength=3")
		require.NotNil(t, cfg)
		assert.Equal(t, 44100, cfg.SampleRate)
		assert.Equal(t, 2, cfg.ChannelConfig)
	})
}

// TestAACDepacketizer_NoPops is the pop-sound check. The two ways this
// pipeline produces audible clicks/pops are:
//
//  1. A dropped access unit leaves a ~21 ms gap (one AAC frame) → a click.
//  2. AU-header bytes leaking into the access unit corrupt the frame → a click.
//  3. A timestamp skip/overlap underruns or duplicates the decoder buffer → a click.
//
// This test synthesizes the AAC-hbr stream ffmpeg emits (one frame per RTP
// packet, 48 kHz clock) and asserts all three are absent.
func TestAACDepacketizer_NoPops(t *testing.T) {
	const (
		clockRate = 48000
		frames    = 8
	)
	depack := newAACDepacketizer(fmtpAAC48k, clockRate)

	auFor := func(i int) []byte {
		// Distinct, recognizable content so corruption or a dropped frame is
		// detectable byte-for-byte.
		return bytes.Repeat([]byte{0xA0 + byte(i)}, 64+i)
	}

	var allAus []aacAccessUnit
	for i := 0; i < frames; i++ {
		payload := buildMpeg4Generic([][]byte{auFor(i)}, 13, 3)
		// RTP timestamp advances by exactly one AAC frame (1024 samples): the
		// stream is gap-free in clock-tick space.
		ts := uint32(i * aacFrameSamples)

		aus, err := depack.depacketize(payload, ts)
		require.NoError(t, err)
		require.Lenf(t, aus, 1, "packet %d: a missing AU gaps the audio (a pop)", i)

		allAus = append(allAus, aus...)
	}

	// (1) No frame dropped: exactly `frames` access units came through.
	assert.Len(t, allAus, frames, "dropped frames gap the audio")

	// (2) No corruption: each AU is byte-identical to what was sent. Stray
	//     AU-header bytes would make the decoder click.
	for i, au := range allAus {
		assert.Equalf(t, auFor(i), au.data,
			"frame %d corrupted: AU-header bytes leaked into the access unit", i)
	}

	// (3) No timestamp discontinuity: every frame lands at its exact slot.
	//     Frame i is at RTP tick i*1024; mapping to µs with the depacketizer's
	//     own conversion makes this exact. A skip or overlap would pop.
	for i, au := range allAus {
		wantPTS := int64(i*aacFrameSamples) * 1_000_000 / int64(clockRate)
		assert.Equalf(t, wantPTS, au.pts,
			"frame %d placed at the wrong time; a skip or overlap pops", i)
	}
}

// TestAACDepacketizer_MultipleAUsPerPacket covers the case where a single RTP
// packet carries several access units. Only the first inherits the packet
// timestamp; the rest advance by one AAC frame each.
func TestAACDepacketizer_MultipleAUsPerPacket(t *testing.T) {
	const clockRate = 48000
	depack := newAACDepacketizer(fmtpAAC48k, clockRate)

	au0 := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	au1 := []byte{0x01, 0x02}
	au2 := bytes.Repeat([]byte{0x55}, 9)

	payload := buildMpeg4Generic([][]byte{au0, au1, au2}, 13, 3)
	const ts = 1_000_000
	aus, err := depack.depacketize(payload, ts)
	require.NoError(t, err)
	require.Len(t, aus, 3)

	// Content integrity, in order.
	assert.Equal(t, au0, aus[0].data)
	assert.Equal(t, au1, aus[1].data)
	assert.Equal(t, au2, aus[2].data)

	// Timestamps: base from the packet, then +1 and +2 frame durations.
	frameDur := aacFrameDurationMicros(clockRate)
	base := int64(ts) * 1_000_000 / int64(clockRate)
	assert.Equal(t, base, aus[0].pts)
	assert.Equal(t, base+frameDur, aus[1].pts)
	assert.Equal(t, base+2*frameDur, aus[2].pts)
}

// TestAACDepacketizer_FmtpFieldWidths confirms the depacketizer honors
// non-default sizelength/indexlength advertised in the fmtp.
func TestAACDepacketizer_FmtpFieldWidths(t *testing.T) {
	const fmtp = "mode=AAC-hbr; sizelength=6; indexlength=2; indexdeltalength=2; config=1190"
	depack := newAACDepacketizer(fmtp, 48000)

	au := []byte{0x11, 0x22, 0x33}
	payload := buildMpeg4Generic([][]byte{au}, 6, 2)

	aus, err := depack.depacketize(payload, 0)
	require.NoError(t, err)
	require.Len(t, aus, 1)
	assert.Equal(t, au, aus[0].data)
}

func TestAACDepacketizer_Errors(t *testing.T) {
	depack := newAACDepacketizer(fmtpAAC48k, 48000)

	t.Run("payload too short", func(t *testing.T) {
		_, err := depack.depacketize([]byte{0x00}, 0)
		assert.ErrorIs(t, err, errShortRTPPayload)
	})

	t.Run("truncated AU data", func(t *testing.T) {
		// Header claims 1 AU of size 64 but no data follows.
		payload := buildMpeg4Generic([][]byte{make([]byte, 64)}, 13, 3)
		// Strip the declared AU data, leaving only the header section.
		_, err := depack.depacketize(payload[:2+((16+7)/8)], 0)
		assert.ErrorIs(t, err, errShortRTPPayload)
	})

	t.Run("zero size length", func(t *testing.T) {
		bad := newAACDepacketizer("mode=AAC-hbr; sizelength=0; indexlength=3", 48000)
		_, err := bad.depacketize(buildMpeg4Generic([][]byte{{0x01}}, 0, 3), 0)
		assert.ErrorIs(t, err, errBadAUHeaderField)
	})
}

// buildMpeg4Generic constructs an mpeg4-generic (RFC 3640) RTP payload for the
// given access units, the inverse of [aacDepacketizer.depacketize]. It is the
// test's notion of what ffmpeg puts on the wire.
func buildMpeg4Generic(aus [][]byte, sizeLength, indexLength int) []byte {
	headerBits := len(aus) * (sizeLength + indexLength)
	headerBytes := (headerBits + 7) / 8

	totalAU := 0
	for _, au := range aus {
		totalAU += len(au)
	}

	out := make([]byte, 2+headerBytes+totalAU)
	// AU-headers-length: size of the AU-headers in bits.
	out[0] = byte(headerBits >> 8)
	out[1] = byte(headerBits)

	// AU-headers: each is [sizeLength bits size][indexLength bits index].
	bitOff := 16
	for _, au := range aus {
		writeBits(out, bitOff, sizeLength, uint(len(au)))
		bitOff += sizeLength
		writeBits(out, bitOff, indexLength, 0)
		bitOff += indexLength
	}

	// Access-unit data, concatenated after the (byte-padded) header section.
	off := 2 + headerBytes
	for _, au := range aus {
		off += copy(out[off:], au)
	}
	return out
}

// writeBits writes v's n bits, MSB-first, into data starting at bitOff.
func writeBits(data []byte, bitOff, n int, v uint) {
	for i := 0; i < n; i++ {
		bitIdx := bitOff + i
		byteIdx := bitIdx >> 3
		bitInByte := 7 - (bitIdx & 7)
		if (v>>(n-1-i))&1 == 1 {
			data[byteIdx] |= 1 << bitInByte
		}
	}
}
