package rtmp

import (
	"encoding/binary"
	"io"
)

const chunkStreamIDControl chunkStreamID = 2

type chunkStreamID = uint32

const messageStreamIDControl StreamID = 0

type chunkHeader struct {
	fmt           uint8
	chunkStreamID chunkStreamID
}

func (h chunkHeader) encode(w io.Writer) error {
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

func (h *chunkHeader) decode(r io.Reader) error {
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

type messageHeader struct {
	timestamp       uint32
	messageLength   int
	messageTypeID   uint8
	messageStreamID StreamID
}

type chunkType uint8

const (
	chunkTimestampMax  uint32    = 0xFFFFFF
	initChunkType      chunkType = 0
	varLenChunkType    chunkType = 1
	timeDeltaChunkType chunkType = 2
	contChunkType      chunkType = 3
)

func encodeChunkTimestamp(w io.Writer, timestamp uint32) error {
	if timestamp < chunkTimestampMax {
		return nil
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], timestamp)
	_, err := w.Write(buf[:])
	return err
}

func decodeChunkTimestamp(timestampField uint32, r io.Reader) (uint32, error) {
	if timestampField < chunkTimestampMax {
		return timestampField, nil
	}
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}
