package cmaf_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/internal/cmaf"
)

// varint encodes a QUIC variable-length integer the way the publisher does: the
// top two bits of the first byte give the encoding's length, the rest are the
// value's most significant bits.
func varint(v uint64) []byte {
	switch {
	case v <= 63:
		return []byte{byte(v)}
	case v <= 16383:
		return []byte{byte(v>>8) | 0x40, byte(v)}
	case v <= 1073741823:
		return []byte{byte(v>>24) | 0x80, byte(v >> 16), byte(v >> 8), byte(v)}
	default:
		return []byte{
			byte(v>>56) | 0xc0, byte(v >> 48), byte(v >> 40), byte(v >> 32),
			byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
		}
	}
}

// locFrame serialises one frame the way MediaFrame does in the browser.
func locFrame(timestamp uint64, payload []byte) []byte {
	out := varint(timestamp)
	out = append(out, varint(uint64(len(payload)))...)
	return append(out, payload...)
}

// Every varint width has to decode, because a timestamp crosses each boundary as
// a stream runs: microseconds pass 63 immediately, 16383 within a frame, and
// 1073741823 after eighteen minutes.
func TestDecodeLOC(t *testing.T) {
	payload := []byte("encoded-frame-bytes")

	tests := map[string]uint64{
		"1-byte varint": 42,
		"2-byte varint": 16_000,
		"4-byte varint": 1_000_000_000,
		"8-byte varint": 5_000_000_000,
		"zero":          0,
	}

	for name, timestamp := range tests {
		t.Run(name, func(t *testing.T) {
			got, data, err := cmaf.DecodeLOC(locFrame(timestamp, payload))

			require.NoError(t, err)
			assert.Equal(t, timestamp, got)
			assert.Equal(t, payload, data)
		})
	}
}

// EncodeLOC must produce the same bytes this test's independent locFrame does,
// so a frame the seeder builds with EncodeLOC is the one DecodeLOC reads.
// locFrame is hand-written here (it does not call EncodeLOC), so this catches a
// bug the encoder and decoder could share. Each varint width is covered, and
// because TestDecodeLOC already round-trips locFrame, EncodeLOC round-trips too.
func TestEncodeLOC(t *testing.T) {
	payload := []byte("encoded-frame-bytes")

	tests := map[string]uint64{
		"1-byte varint": 42,
		"2-byte varint": 16_000,
		"4-byte varint": 1_000_000_000,
		"8-byte varint": 5_000_000_000,
		"zero":          0,
	}

	for name, timestamp := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, locFrame(timestamp, payload), cmaf.EncodeLOC(timestamp, payload))
		})
	}
}

// A frame carrying no payload is still a well-formed frame; it must not be read
// as a truncated one.
func TestDecodeLOC_EmptyPayload(t *testing.T) {
	timestamp, data, err := cmaf.DecodeLOC(locFrame(1234, nil))

	require.NoError(t, err)
	assert.Equal(t, uint64(1234), timestamp)
	assert.Empty(t, data)
}

// Trailing bytes past the declared size are not part of the frame. Returning
// them would hand the muxer whatever followed on the wire.
func TestDecodeLOC_StopsAtDeclaredSize(t *testing.T) {
	frame := append(locFrame(7, []byte("abc")), []byte("trailing")...)

	_, data, err := cmaf.DecodeLOC(frame)

	require.NoError(t, err)
	assert.Equal(t, []byte("abc"), data)
}

// Anything that does not hold what it claims is rejected rather than decoded
// into a frame made of adjacent bytes.
func TestDecodeLOC_Rejects(t *testing.T) {
	tests := map[string][]byte{
		"empty":                         nil,
		"timestamp only":                varint(42),
		"varint cut short":              {0x40},                   // announces 2 bytes, carries 1
		"size varint missing":           append(varint(42), 0x80), // announces 4 bytes, carries 1
		"payload shorter than declared": append(append(varint(42), varint(100)...), []byte("short")...),
	}

	for name, frame := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := cmaf.DecodeLOC(frame)
			assert.Error(t, err)
		})
	}
}
