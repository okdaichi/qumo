package rtsp

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rtpHeader builds the fixed 12-byte RTP header. CC = contributing-source count,
// ext/pad/mark set the corresponding bits.
func rtpHeader(cc int, ext, pad, mark bool, pt uint8, seq uint16, ts, ssrc uint32) []byte {
	b := make([]byte, 12)
	b[0] = byte(2<<6) | // version 2
		bo(pad, 5) | bo(ext, 4) | byte(cc&0x0F)
	b[1] = bo(mark, 7) | (pt & 0x7F)
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[4:8], ts)
	binary.BigEndian.PutUint32(b[8:12], ssrc)
	return b
}

func bo(bit bool, pos uint8) byte {
	if bit {
		return 1 << pos
	}
	return 0
}

func TestUnmarshalRTP(t *testing.T) {
	t.Run("basic packet (no CSRC/ext/pad)", func(t *testing.T) {
		data := append(rtpHeader(0, false, false, true, 96, 1234, 5678, 9012), 0xDE, 0xAD, 0xBE, 0xEF)
		got, err := UnmarshalRTP(data)
		require.NoError(t, err)
		assert.Equal(t, RTPHeader{Version: 2, Marker: true, PayloadType: 96,
			SequenceNumber: 1234, Timestamp: 5678, SSRC: 9012}, got.Header)
		assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, got.Payload)
	})

	t.Run("CSRC list is skipped", func(t *testing.T) {
		// CC=2 → two 4-byte CSRC identifiers before the payload.
		hdr := rtpHeader(2, false, false, false, 96, 1, 0, 0)
		data := append(hdr,
			0x01, 0x02, 0x03, 0x04, // CSRC 1
			0x05, 0x06, 0x07, 0x08, // CSRC 2
			0xAA, 0xBB, // payload
		)
		got, err := UnmarshalRTP(data)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xAA, 0xBB}, got.Payload, "payload excludes CSRC list")
	})

	t.Run("header extension is skipped", func(t *testing.T) {
		hdr := rtpHeader(0, true, false, false, 96, 1, 0, 0)
		ext := make([]byte, 8) // 4-byte ext header (profile+len=1 word) + 4-byte body
		binary.BigEndian.PutUint16(ext[2:4], 1)
		data := append(append(hdr, ext...), 0xCC, 0xDD)
		got, err := UnmarshalRTP(data)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xCC, 0xDD}, got.Payload, "payload excludes extension header + body")
	})

	t.Run("padding is stripped", func(t *testing.T) {
		hdr := rtpHeader(0, false, true, false, 96, 1, 0, 0)
		// payload (2 bytes) + 3 padding bytes (last byte = pad count = 3).
		data := append(hdr, 0xAA, 0xBB, 0x00, 0x00, 0x03)
		got, err := UnmarshalRTP(data)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xAA, 0xBB}, got.Payload, "trailing padding removed")
	})

	t.Run("CSRC + extension + padding together", func(t *testing.T) {
		hdr := rtpHeader(1, true, true, true, 96, 1, 0, 0)
		ext := make([]byte, 8)
		binary.BigEndian.PutUint16(ext[2:4], 1)
		data := append(hdr,
			0x11, 0x11, 0x11, 0x11, // CSRC
		)
		data = append(data, ext...)                 // extension
		data = append(data, 0x42, 0x42, 0x00, 0x02) // payload (2) + 2 pad bytes
		got, err := UnmarshalRTP(data)
		require.NoError(t, err)
		assert.Equal(t, []byte{0x42, 0x42}, got.Payload, "payload excludes CSRC, extension, and padding")
		assert.True(t, got.Header.Extension && got.Header.Padding && got.Header.Marker)
	})

	t.Run("too short", func(t *testing.T) {
		_, err := UnmarshalRTP([]byte{0x80, 0x60, 0, 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too short")
	})

	t.Run("CSRC list overruns packet", func(t *testing.T) {
		_, err := UnmarshalRTP(rtpHeader(3, false, false, false, 96, 1, 0, 0))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CSRC")
	})

	t.Run("extension header overruns packet", func(t *testing.T) {
		// Extension bit set but only 2 bytes follow the fixed header.
		data := append(rtpHeader(0, true, false, false, 96, 1, 0, 0), 0x00, 0x00)
		_, err := UnmarshalRTP(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension")
	})

	t.Run("extension body overruns packet", func(t *testing.T) {
		hdr := rtpHeader(0, true, false, false, 96, 1, 0, 0)
		ext := make([]byte, 4)
		binary.BigEndian.PutUint16(ext[2:4], 5) // claims 5 words of body, but none follows
		data := append(hdr, ext...)
		_, err := UnmarshalRTP(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension")
	})

	t.Run("padding length out of range", func(t *testing.T) {
		hdr := rtpHeader(0, false, true, false, 96, 1, 0, 0)
		_, err := UnmarshalRTP(append(hdr, 0xFF)) // pad count 255 > 1 available byte
		require.Error(t, err)
		assert.Contains(t, err.Error(), "padding")
	})
}
