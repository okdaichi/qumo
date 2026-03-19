package rtmp

import (
	"bytes"
	"errors"
	"io"
	"time"
)

const chunkStreamIDControl chunkStreamID = 2

type chunkStreamID = uint32

const messageStreamIDControl StreamID = 0

type chunkBasicHeader struct {
	fmt           uint8
	chunkStreamID chunkStreamID
}

func (cbh chunkBasicHeader) encode(w io.Writer) error {
	var buf [3]byte
	var length int
	if cbh.chunkStreamID <= 63 {
		buf[0] = (cbh.fmt << 6) | uint8(cbh.chunkStreamID)
		length = 1
	} else if cbh.chunkStreamID <= 319 {
		buf[0] = (cbh.fmt << 6)
		buf[1] = uint8(cbh.chunkStreamID - 64)
		length = 2
	} else {
		buf[0] = (cbh.fmt << 6) | 1
		buf[1] = uint8(cbh.chunkStreamID - 64)
		buf[2] = uint8((cbh.chunkStreamID - 64) >> 8)
		length = 3
	}
	_, err := w.Write(buf[:length])
	return err
}

func (cbh *chunkBasicHeader) decode(r io.Reader) error {
	var buf [3]byte
	_, err := io.ReadFull(r, buf[:1])
	if err != nil {
		return err
	}
	cbh.fmt = buf[0] >> 6
	streamIDPart := buf[0] & 0x3F
	switch streamIDPart {
	case 0:
		_, err := io.ReadFull(r, buf[:1])
		if err != nil {
			return err
		}
		cbh.chunkStreamID = uint32(buf[0]) + 64
	case 1:
		_, err := io.ReadFull(r, buf[:2])
		if err != nil {
			return err
		}
		cbh.chunkStreamID = uint32(buf[0]) + (uint32(buf[1]) << 8) + 64
	default:
		cbh.chunkStreamID = uint32(streamIDPart)
	}
	return nil
}

type encodedMessage io.Reader

type chunkType uint8

const (
	initChunkType      chunkType = 0
	varLenChunkType    chunkType = 1
	timeDeltaChunkType chunkType = 2
	contChunkType      chunkType = 3
)

type chunkStreamInit struct {
	timestamp       uint32
	messageLen      uint32
	messageTypeID   uint8
	messageStreamID uint32
}

func newMessageEncoder(streamID chunkStreamID, maxChunkSize int, init *chunkStreamInit) *messageEncoder {
	return &messageEncoder{
		streamID: streamID,
		init:     init,
	}
}

type messageEncoder struct {
	streamID        chunkStreamID
	init            *chunkStreamInit
	latestTimestamp time.Time
	timeDelta       time.Duration

	payload bytes.Buffer

	chunkBuf []byte
}

func (cw *messageEncoder) writeMessage(w io.Writer) error {
	for {
		var header chunkBasicHeader
		if cw.init == nil {
			header = chunkBasicHeader{
				fmt:           uint8(initChunkType),
				chunkStreamID: cw.streamID,
			}
		} else if cw.timeDelta > 0 {
			header = chunkBasicHeader{
				fmt:           uint8(timeDeltaChunkType),
				chunkStreamID: cw.streamID,
			}
		}

		if err := header.encode(w); err != nil {
			return err
		}

		chunkSize := min(cw.unreadBytes(), len(cw.chunkBuf))
		if chunkSize == 0 {
			break
		}
		chunkBuf := cw.chunkBuf[:chunkSize]

		_, err := cw.payload.Read(chunkBuf)
		if err != nil && err != io.EOF {
			// Ignore EOF since it just means we've read all the data in the payload buffer, which is expected.
			return err
		}
		_, err = w.Write(chunkBuf)
		if err == io.EOF {
			// Message fully written
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (cw *messageEncoder) unreadBytes() int {
	return cw.payload.Len()
}

func newMessageDecoder(streamID chunkStreamID, init chunkStreamInit) *messageDecoder {
	return &messageDecoder{
		chunkStreamID: streamID,
		init:          init,
	}
}

type messageDecoder struct {
	chunkStreamID   chunkStreamID
	init            chunkStreamInit
	latestTimestamp uint32
	timeDelta       uint32

	payload *bytes.Buffer
}

func (cd *messageDecoder) Bytes() []byte {
	return cd.payload.Bytes()
}

func (cd *messageDecoder) completed() bool {
	// A message is considered "completed" when we've read enough bytes to complete the message based on the length specified in the init struct.
	return uint32(cd.payload.Len()) >= cd.init.messageLen
}

var ErrMessageTooLong = errors.New("message too long")

func (cd *messageDecoder) appendChunk(chunk []byte) error {
	if cd.completed() {
		return ErrMessageTooLong
	}
	if cd.payload.Len()+len(chunk) > int(cd.init.messageLen) {
		return ErrMessageTooLong
	}
	_, err := cd.payload.Write(chunk)
	return err
}

func (cd *messageDecoder) unreadBytes() int {
	return int(cd.init.messageLen) - cd.payload.Len()
}
