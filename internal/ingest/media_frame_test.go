package ingest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuicVarintLen(t *testing.T) {
	tests := map[string]struct {
		val  uint64
		want int
	}{
		"zero":        {0, 1},
		"max 1-byte":  {63, 1},
		"min 2-byte":  {64, 2},
		"max 2-byte":  {16383, 2},
		"min 4-byte":  {16384, 4},
		"max 4-byte":  {1073741823, 4},
		"min 8-byte":  {1073741824, 8},
		"large value": {1_000_000_000_000, 8},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, quicVarintLen(tt.val))
		})
	}
}

func TestPutQuicVarint_RoundTrip(t *testing.T) {
	values := []uint64{0, 1, 63, 64, 16383, 16384, 1073741823, 1073741824, 1_000_000}

	for _, v := range values {
		buf := make([]byte, 8)
		n := putQuicVarint(buf, v)
		assert.Equal(t, quicVarintLen(v), n, "value=%d", v)

		// Verify the 2-bit prefix encodes the correct length.
		prefix := buf[0] >> 6
		expectedLen := 1 << prefix
		assert.Equal(t, n, expectedLen, "value=%d", v)

		// Decode and verify round-trip.
		got := decodeQuicVarint(buf[:n])
		assert.Equal(t, v, got, "value=%d", v)
	}
}

// decodeQuicVarint is a test helper that decodes a QUIC varint for verification.
func decodeQuicVarint(buf []byte) uint64 {
	prefix := buf[0] >> 6
	buf[0] &= 0x3F // mask off the 2-bit length prefix
	switch prefix {
	case 0:
		return uint64(buf[0])
	case 1:
		return uint64(buf[0])<<8 | uint64(buf[1])
	case 2:
		return uint64(buf[0])<<24 | uint64(buf[1])<<16 | uint64(buf[2])<<8 | uint64(buf[3])
	case 3:
		return uint64(buf[0])<<56 | uint64(buf[1])<<48 | uint64(buf[2])<<40 | uint64(buf[3])<<32 |
			uint64(buf[4])<<24 | uint64(buf[5])<<16 | uint64(buf[6])<<8 | uint64(buf[7])
	}
	return 0
}

func TestBuildMediaFrame(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	timestampUS := int64(42_000) // 42ms in μs

	frame := buildMediaFrame(timestampUS, data)

	// Decode and verify.
	off := 0

	// Read timestamp varint.
	tsLen := quicVarintLen(uint64(timestampUS))
	tsBuf := make([]byte, tsLen)
	copy(tsBuf, frame[off:off+tsLen])
	gotTS := decodeQuicVarint(tsBuf)
	assert.Equal(t, uint64(timestampUS), gotTS)
	off += tsLen

	// Read data length varint.
	dataLenLen := quicVarintLen(uint64(len(data)))
	dlBuf := make([]byte, dataLenLen)
	copy(dlBuf, frame[off:off+dataLenLen])
	gotDL := decodeQuicVarint(dlBuf)
	assert.Equal(t, uint64(len(data)), gotDL)
	off += dataLenLen

	// Read data.
	assert.Equal(t, data, frame[off:off+len(data)])
	assert.Equal(t, len(frame), off+len(data))
}

func TestMediaFrameSize(t *testing.T) {
	data := make([]byte, 100)
	ts := int64(1_000_000) // 1 second in μs

	size := mediaFrameSize(ts, len(data))
	frame := buildMediaFrame(ts, data)

	assert.Equal(t, size, len(frame))
}

func TestEncodeMediaFrame_ZeroTimestamp(t *testing.T) {
	data := []byte{0x01}
	frame := buildMediaFrame(0, data)
	require.Len(t, frame, 3)              // 1 (ts=0) + 1 (len=1) + 1 (data)
	assert.Equal(t, byte(0x00), frame[0]) // timestamp = 0
	assert.Equal(t, byte(0x01), frame[1]) // data length = 1
	assert.Equal(t, byte(0x01), frame[2]) // data
}
