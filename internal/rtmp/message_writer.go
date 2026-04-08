package rtmp

import (
	"bytes"
	"io"
)

func newSendChunkStream(streamID chunkStreamID, timestamp uint32, messageLen uint32, messageTypeID uint8, messageStreamID uint32) *sendChunkStream {
	return &sendChunkStream{
		chunkStreamID:   streamID,
		timestamp:       timestamp,
		messageLen:      messageLen,
		messageTypeID:   messageTypeID,
		messageStreamID: messageStreamID,
	}
}

type sendChunkStream struct {
	chunkStreamID chunkStreamID
	// timestamp       uint32
	// messageLen      uint32
	// messageTypeID   uint8
	// messageStreamID uint32
	messageHeaderManager messageHeaderManager

	payload *bytes.Buffer

	chunkBuf []byte
}

func (cw *sendChunkStream) writeMessage(w io.Writer) error {
	if cw.payload == nil {
		cw.payload = &bytes.Buffer{}
	}
	if cw.chunkBuf == nil {
		cw.chunkBuf = make([]byte, DefaultChunkSize)
	}

	remaining := cw.payload.Len()
	if remaining == 0 {
		return nil
	}
	if cw.messageLen == 0 {
		cw.messageLen = uint32(remaining)
	}

	written := 0
	for {
		var header chunkHeader
		if written == 0 {
			header = chunkHeader{
				fmt:           uint8(initChunkType),
				chunkStreamID: cw.chunkStreamID,
			}
		} else {
			header = chunkHeader{
				fmt:           uint8(contChunkType),
				chunkStreamID: cw.chunkStreamID,
			}
		}

		if err := header.encode(w); err != nil {
			return err
		}
		if written == 0 {
			var msgHeader [11]byte
			if cw.timestamp >= chunkTimestampMax {
				msgHeader[0] = 0xFF
				msgHeader[1] = 0xFF
				msgHeader[2] = 0xFF
			} else {
				msgHeader[0] = byte(cw.timestamp >> 16)
				msgHeader[1] = byte(cw.timestamp >> 8)
				msgHeader[2] = byte(cw.timestamp)
			}
			msgHeader[3] = byte(cw.messageLen >> 16)
			msgHeader[4] = byte(cw.messageLen >> 8)
			msgHeader[5] = byte(cw.messageLen)
			msgHeader[6] = cw.messageTypeID
			msgHeader[7] = byte(cw.messageStreamID)
			msgHeader[8] = byte(cw.messageStreamID >> 8)
			msgHeader[9] = byte(cw.messageStreamID >> 16)
			msgHeader[10] = byte(cw.messageStreamID >> 24)
			if _, err := w.Write(msgHeader[:]); err != nil {
				return err
			}
			if cw.timestamp >= chunkTimestampMax {
				if err := encodeChunkTimestamp(w, cw.timestamp); err != nil {
					return err
				}
			}
		} else if cw.timestamp >= chunkTimestampMax {
			if err := encodeChunkTimestamp(w, cw.timestamp); err != nil {
				return err
			}
		}

		chunkSize := min(remaining-written, len(cw.chunkBuf))
		if chunkSize == 0 {
			break
		}
		chunkBuf := cw.chunkBuf[:chunkSize]

		if _, err := io.ReadFull(cw.payload, chunkBuf); err != nil {
			return err
		}
		_, err := w.Write(chunkBuf)
		if err != nil {
			return err
		}
		written += chunkSize
		if written >= remaining {
			break
		}
	}

	return nil
}

type sendMessageStream struct {
	streamID StreamID
	messages *messageRingBuffer
	encoder  *sendChunkStream
}

func newSendMessageStream(streamID StreamID) *sendMessageStream {
	return &sendMessageStream{
		streamID: streamID,
		messages: newMessageRingBuffer(DefaultMessageBufferSize),
		encoder:  newSendChunkStream(0, 0, 0, 0, 0),
	}
}

func (s *sendMessageStream) Enqueue(m *message) bool {
	if s == nil {
		return false
	}
	if s.messages == nil {
		s.messages = newMessageRingBuffer(DefaultMessageBufferSize)
	}
	return s.messages.Push(m)
}

func (s *sendMessageStream) Dequeue() (*message, bool) {
	if s == nil || s.messages == nil {
		return nil, false
	}
	return s.messages.Pop()
}

func (s *sendMessageStream) Reset() {
	if s == nil || s.messages == nil {
		return
	}
	s.messages.Reset()
}

func (s *sendMessageStream) WriteMessage(w io.Writer, chunkStreamID chunkStreamID, maxChunkSize int) error {
	msg, ok := s.Dequeue()
	if !ok {
		return io.EOF
	}
	defer releaseMessage(msg)

	if maxChunkSize <= 0 {
		maxChunkSize = int(DefaultChunkSize)
	}

	if s.encoder == nil {
		s.encoder = newSendChunkStream(0, 0, 0, 0, 0)
	}
	s.encoder.chunkStreamID = chunkStreamID
	s.encoder.timestamp = msg.timestamp
	s.encoder.messageLen = uint32(msg.payload.Len())
	s.encoder.messageTypeID = uint8(msg.messageTypeID)
	s.encoder.messageStreamID = uint32(msg.messageStreamID)
	s.encoder.payload = msg.payload
	if cap(s.encoder.chunkBuf) < maxChunkSize {
		s.encoder.chunkBuf = make([]byte, maxChunkSize)
	} else {
		s.encoder.chunkBuf = s.encoder.chunkBuf[:maxChunkSize]
	}
	return s.encoder.writeMessage(w)
}
