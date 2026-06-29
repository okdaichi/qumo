package integration

import (
	"encoding/binary"
	"errors"
)

// MediaFrame envelope errors.
var (
	ErrShortMediaFrame = errors.New("interop: media frame too short")
	ErrBadMediaVarint  = errors.New("interop: malformed QUIC varint in media frame")
)

// readQuicVarint decodes a QUIC variable-length integer (RFC 9000 §16) from the
// start of buf. It returns the value and the number of bytes consumed, or an
// error if buf is empty/truncated. It is the inverse of ingest's putQuicVarint.
func readQuicVarint(buf []byte) (val uint64, n int, err error) {
	if len(buf) == 0 {
		return 0, 0, ErrBadMediaVarint
	}
	prefix := buf[0] >> 6
	length := 1 << prefix // 1, 2, 4, or 8
	if len(buf) < length {
		return 0, 0, ErrBadMediaVarint
	}
	// Mask off the 2-bit length prefix from the first byte.
	v := uint64(buf[0] & 0x3F)
	for i := 1; i < length; i++ {
		v = v<<8 | uint64(buf[i])
	}
	return v, length, nil
}

// decodeMediaFrame parses a MediaFrame envelope ([QUIC varint: timestamp_μs]
// [QUIC varint: data_length][data]) — the format ingest emits via
// ingest.writeMediaFrame — returning the presentation timestamp in microseconds
// and the codec-specific frame data (AVCC NALUs for video, raw AAC for audio).
func decodeMediaFrame(body []byte) (ptsUS int64, data []byte, err error) {
	ts, n, err := readQuicVarint(body)
	if err != nil {
		return 0, nil, err
	}
	rest := body[n:]
	dataLen, m, err := readQuicVarint(rest)
	if err != nil {
		return 0, nil, err
	}
	rest = rest[m:]
	if uint64(len(rest)) < dataLen {
		return 0, nil, ErrShortMediaFrame
	}
	// ts and dataLen are QUIC varints, whose encoding bounds them at 2^62-1
	// (see readQuicVarint). That is well within int64's 2^63-1 range, so this
	// conversion cannot overflow.
	return int64(ts), rest[:dataLen], nil
}

// isAVCCKeyframe reports whether an AVCC frame (4-byte length-prefixed NALUs)
// contains an IDR slice (NAL unit type 5). Used by the collector to count
// keyframes. It assumes the 4-byte length prefix qumo emits (NALULenSize 4);
// frames that are too short or malformed are reported as non-keyframes.
func isAVCCKeyframe(avcc []byte) bool {
	for off := 0; off+4 <= len(avcc); {
		naluLen := int(binary.BigEndian.Uint32(avcc[off:]))
		off += 4
		if off > len(avcc) || off+naluLen > len(avcc) || naluLen == 0 {
			return false
		}
		if avcc[off]&0x1F == 5 { // NAL unit type 5 = IDR slice
			return true
		}
		off += naluLen
	}
	return false
}
