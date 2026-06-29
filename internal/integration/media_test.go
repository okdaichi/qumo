package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putVarint mirrors ingest.putQuicVarint for self-contained round-trip tests.
func putVarint(dst []byte, v uint64) int {
	switch {
	case v <= 63:
		dst[0] = byte(v)
		return 1
	case v <= 16383:
		dst[0] = byte(v>>8) | 0x40
		dst[1] = byte(v)
		return 2
	case v <= 1073741823:
		dst[0] = byte(v>>24) | 0x80
		dst[1] = byte(v >> 16)
		dst[2] = byte(v >> 8)
		dst[3] = byte(v)
		return 4
	default:
		dst[0] = byte(v>>56) | 0xC0
		dst[1] = byte(v >> 48)
		dst[2] = byte(v >> 40)
		dst[3] = byte(v >> 32)
		dst[4] = byte(v >> 24)
		dst[5] = byte(v >> 16)
		dst[6] = byte(v >> 8)
		dst[7] = byte(v)
		return 8
	}
}

func TestReadQuicVarint(t *testing.T) {
	tests := []uint64{0, 1, 63, 64, 16383, 16384, 1_000_000, 1073741823, 1 << 40}
	for _, v := range tests {
		buf := make([]byte, 8)
		n := putVarint(buf, v)
		got, consumed, err := readQuicVarint(buf[:n])
		require.NoError(t, err, "value=%d", v)
		assert.Equal(t, n, consumed, "value=%d", v)
		assert.Equal(t, v, got, "value=%d", v)
	}
}

func TestReadQuicVarint_Errors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, _, err := readQuicVarint(nil)
		assert.ErrorIs(t, err, ErrBadMediaVarint)
	})
	t.Run("truncated 8-byte form", func(t *testing.T) {
		// First byte 0xFF → 8-byte form, but only 3 bytes supplied.
		_, _, err := readQuicVarint([]byte{0xFF, 0x01, 0x02})
		assert.ErrorIs(t, err, ErrBadMediaVarint)
	})
}

func TestDecodeMediaFrame(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	// Build envelope [varint ts][varint len][data].
	var env []byte
	b := make([]byte, 8)
	n := putVarint(b, 1_000_000) // 1s in µs
	env = append(env, b[:n]...)
	n = putVarint(b, uint64(len(data)))
	env = append(env, b[:n]...)
	env = append(env, data...)

	pts, got, err := decodeMediaFrame(env)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000_000), pts)
	assert.Equal(t, data, got)
}

func TestDecodeMediaFrame_Errors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, _, err := decodeMediaFrame(nil)
		assert.Error(t, err)
	})
	t.Run("declared length exceeds body", func(t *testing.T) {
		var env []byte // separate backing array to avoid aliasing the scratch buffer
		tmp := make([]byte, 8)
		n := putVarint(tmp, 50) // ts
		env = append(env, tmp[:n]...)
		n = putVarint(tmp, 64) // claims 64 data bytes
		env = append(env, tmp[:n]...)
		env = append(env, 0x01, 0x02) // only 2 follow
		_, _, err := decodeMediaFrame(env)
		assert.ErrorIs(t, err, ErrShortMediaFrame)
	})
}

func TestIsAVCCKeyframe(t *testing.T) {
	nalu := func(typ byte, payload ...byte) []byte {
		n := append([]byte{typ}, payload...)
		out := make([]byte, 4+len(n))
		out[0], out[1], out[2], out[3] = byte(len(n)>>24), byte(len(n)>>16), byte(len(n)>>8), byte(len(n))
		copy(out[4:], n)
		return out
	}
	t.Run("IDR (type 5) is keyframe", func(t *testing.T) {
		assert.True(t, isAVCCKeyframe(nalu(0x65, 0xAA, 0xBB)))
	})
	t.Run("non-IDR (type 1) is not", func(t *testing.T) {
		assert.False(t, isAVCCKeyframe(nalu(0x41, 0x01)))
	})
	t.Run("IDR among multiple NALUs", func(t *testing.T) {
		avcc := append(nalu(0x09, 0x10), nalu(0x65, 0xCC)...) // AUD + IDR
		assert.True(t, isAVCCKeyframe(avcc))
	})
	t.Run("too short is not", func(t *testing.T) {
		assert.False(t, isAVCCKeyframe([]byte{0x00, 0x00}))
	})
}
