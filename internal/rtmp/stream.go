package rtmp

import (
	"encoding/binary"
	"io"
)

// chunkStreamID is a type alias for chunk stream identifiers.
type chunkStreamID = uint32

// chunkBasicHeader carries the fmt (chunk type) and chunk stream ID.
type chunkBasicHeader struct {
	fmt           uint8
	chunkStreamID chunkStreamID
}

func (h chunkBasicHeader) encode(w io.Writer) error {
	var buf [3]byte
	var length int
	if h.chunkStreamID <= 63 {
		buf[0] = (h.fmt << 6) | uint8(h.chunkStreamID)
		length = 1
	} else if h.chunkStreamID <= 319 {
		buf[0] = (h.fmt << 6)
		buf[1] = uint8(h.chunkStreamID - 64)
		length = 2
	} else {
		buf[0] = (h.fmt << 6) | 1
		buf[1] = uint8(h.chunkStreamID - 64)
		buf[2] = uint8((h.chunkStreamID - 64) >> 8)
		length = 3
	}
	_, err := w.Write(buf[:length])
	return err
}

func (h *chunkBasicHeader) decode(r io.Reader) error {
	var buf [3]byte
	_, err := io.ReadFull(r, buf[:1])
	if err != nil {
		return err
	}
	h.fmt = buf[0] >> 6
	streamIDPart := buf[0] & 0x3F
	switch streamIDPart {
	case 0:
		_, err := io.ReadFull(r, buf[:1])
		if err != nil {
			return err
		}
		h.chunkStreamID = uint32(buf[0]) + 64
	case 1:
		_, err := io.ReadFull(r, buf[:2])
		if err != nil {
			return err
		}
		h.chunkStreamID = uint32(buf[0]) + (uint32(buf[1]) << 8) + 64
	default:
		h.chunkStreamID = uint32(streamIDPart)
	}
	return nil
}

// chunkTimestampMax is 0xFFFFFF, the sentinel value that indicates an
// extended timestamp field follows.
const chunkTimestampMax uint32 = 0xFFFFFF

func encodeExtendedTimestamp(w io.Writer, ts uint32) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], ts)
	_, err := w.Write(buf[:])
	return err
}

func decodeExtendedTimestamp(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}
