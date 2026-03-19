package rtmp

import "io"

const chunkStreamIDControl chunkStreamID = 2

type chunkStreamID = uint32

const messageStreamIDControl StreamID = 0

type chunkBasicHeader struct {
	fmt           uint8
	chunkStreamID chunkStreamID
}

func (cbh chunkBasicHeader) encode(w io.Writer) error {
	var buf []byte
	if cbh.chunkStreamID <= 63 {
		buf = []byte{(cbh.fmt << 6) | uint8(cbh.chunkStreamID)}
	} else if cbh.chunkStreamID <= 319 {
		buf = []byte{(cbh.fmt << 6), uint8(cbh.chunkStreamID - 64)}
	} else {
		buf = []byte{(cbh.fmt << 6) | 1, uint8(cbh.chunkStreamID - 64), uint8((cbh.chunkStreamID - 64) >> 8)}
	}
	_, err := w.Write(buf)
	return err
}

func (cbh *chunkBasicHeader) decode(r io.Reader) error {
	buf := make([]byte, 1)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	cbh.fmt = buf[0] >> 6
	streamIDPart := buf[0] & 0x3F
	switch streamIDPart {
	case 0:
		buf = make([]byte, 1)
		_, err := io.ReadFull(r, buf)
		if err != nil {
			return err
		}
		cbh.chunkStreamID = uint32(buf[0]) + 64
	case 1:
		buf = make([]byte, 2)
		_, err := io.ReadFull(r, buf)
		if err != nil {
			return err
		}
		cbh.chunkStreamID = uint32(buf[0]) + (uint32(buf[1]) << 8) + 64
	default:
		cbh.chunkStreamID = uint32(streamIDPart)
	}
	return nil
}

type chunkTypeID uint8

const (
	initChunkType      chunkTypeID = 0
	varLenChunkType    chunkTypeID = 1
	timeDeltaChunkType chunkTypeID = 2
	contChunkType      chunkTypeID = 3
)

type chunkStream struct {
	streamID chunkStreamID
	message  message
}
