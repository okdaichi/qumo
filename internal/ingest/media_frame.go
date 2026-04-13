package ingest

import "encoding/binary"

// mediaFrameSize returns the total byte length of a MediaFrame-encoded
// payload: [varint:timestamp][varint:dataLen][data].
// Timestamps are in microseconds.
func mediaFrameSize(timestampUS int64, dataLen int) int {
	return quicVarintLen(uint64(timestampUS)) + quicVarintLen(uint64(dataLen)) + dataLen
}

// encodeMediaFrame writes a MediaFrame envelope into dst and returns the
// number of bytes written. The format matches the web client's
// deserializeMediaFrame:
//
//	[QUIC varint: timestamp_μs][QUIC varint: data_length][data]
//
// The caller must ensure dst is large enough (use [mediaFrameSize]).
func encodeMediaFrame(dst []byte, timestampUS int64, data []byte) int {
	n := putQuicVarint(dst, uint64(timestampUS))
	n += putQuicVarint(dst[n:], uint64(len(data)))
	n += copy(dst[n:], data)
	return n
}

// buildMediaFrame allocates and returns a MediaFrame-encoded byte slice.
func buildMediaFrame(timestampUS int64, data []byte) []byte {
	size := mediaFrameSize(timestampUS, len(data))
	buf := make([]byte, size)
	encodeMediaFrame(buf, timestampUS, data)
	return buf
}

// QUIC varint encoding (RFC 9000, Section 16).
// The 2 most-significant bits of the first byte encode the length:
//
//	00 → 1 byte  (6-bit value,  max 63)
//	01 → 2 bytes (14-bit value, max 16383)
//	10 → 4 bytes (30-bit value, max 1073741823)
//	11 → 8 bytes (62-bit value)

func quicVarintLen(v uint64) int {
	switch {
	case v <= 63:
		return 1
	case v <= 16383:
		return 2
	case v <= 1073741823:
		return 4
	default:
		return 8
	}
}

func putQuicVarint(dst []byte, v uint64) int {
	switch {
	case v <= 63:
		dst[0] = byte(v)
		return 1
	case v <= 16383:
		binary.BigEndian.PutUint16(dst, uint16(v)|0x4000)
		return 2
	case v <= 1073741823:
		binary.BigEndian.PutUint32(dst, uint32(v)|0x80000000)
		return 4
	default:
		binary.BigEndian.PutUint64(dst, v|0xC000000000000000)
		return 8
	}
}
