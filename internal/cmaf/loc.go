package cmaf

import (
	"encoding/binary"
	"fmt"
)

// DecodeLOC reads one LOC frame: a QUIC variable-length timestamp in
// microseconds, a variable-length payload size, then the encoded frame.
//
// This is the whole container. A frame carries no duration, no codec
// configuration and no keyframe flag — that is what makes it low overhead, and
// what leaves the packager to recover durations from timestamps and sync points
// from group boundaries. Everything a decoder needs beyond the bytes lives in
// the catalog instead, stated once.
func DecodeLOC(b []byte) (timestamp uint64, payload []byte, err error) {
	timestamp, n, err := varint(b)
	if err != nil {
		return 0, nil, fmt.Errorf("cmaf: loc timestamp: %w", err)
	}
	b = b[n:]

	size, n, err := varint(b)
	if err != nil {
		return 0, nil, fmt.Errorf("cmaf: loc payload size: %w", err)
	}
	b = b[n:]

	if uint64(len(b)) < size {
		return 0, nil, fmt.Errorf("cmaf: loc frame declares %d bytes but holds %d", size, len(b))
	}
	return timestamp, b[:size], nil
}

// varint decodes a QUIC variable-length integer (RFC 9000 §16): the top two bits
// of the first byte give the encoding's length, and the remaining bits are the
// value's most significant.
func varint(b []byte) (value uint64, size int, err error) {
	if len(b) == 0 {
		return 0, 0, fmt.Errorf("no bytes")
	}

	size = 1 << (b[0] >> 6)
	if len(b) < size {
		return 0, 0, fmt.Errorf("need %d bytes, have %d", size, len(b))
	}

	// The length prefix is not part of the value.
	switch size {
	case 1:
		return uint64(b[0] & 0x3f), 1, nil
	case 2:
		return uint64(binary.BigEndian.Uint16(b[:2]) & 0x3fff), 2, nil
	case 4:
		return uint64(binary.BigEndian.Uint32(b[:4]) & 0x3fff_ffff), 4, nil
	default:
		return binary.BigEndian.Uint64(b[:8]) & 0x3fff_ffff_ffff_ffff, 8, nil
	}
}
