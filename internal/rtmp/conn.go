package rtmp

import (
	"errors"
	"io"
	"net"
)

func NewConn(transport net.Conn) *Conn {
	return &Conn{
		transport:           transport,
		receiveChunkStreams: make(map[chunkStreamID]*receiveChunkStream),
		maxChunkSize:        DefaultChunkSize,
	}
}

type Conn struct {
	transport net.Conn

	receiveChunkStreams map[chunkStreamID]*receiveChunkStream

	maxChunkSize ChunkSize
}

var ErrUnsupportedChunkType = errors.New("unsupported chunk type")
var ErrInvalidChunkHeader = errors.New("invalid chunk header")

func (conn *Conn) readChunks() error {
	chunkHeader := chunkHeader{}
	r := conn.transport

	msgHeaderBuf := make([]byte, 11)
	payloadBuf := make([]byte, 0)

	var messageHeader messageHeader

	for {
		if err := chunkHeader.decode(r); err != nil {
			return err
		}

		chunkStream, ok := conn.receiveChunkStreams[chunkHeader.chunkStreamID]
		if !ok {
			chunkStream = newReceiveChunkStream(chunkHeader.chunkStreamID)
			conn.receiveChunkStreams[chunkHeader.chunkStreamID] = chunkStream
		}

		switch chunkType(chunkHeader.fmt) {
		case initChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:11]); err != nil {
				return err
			}
			timestampField := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])
			timestamp, err := decodeChunkTimestamp(timestampField, r)
			if err != nil {
				return err
			}
			msgLen := uint32(msgHeaderBuf[3])<<16 | uint32(msgHeaderBuf[4])<<8 | uint32(msgHeaderBuf[5])
			msgTypeID := msgHeaderBuf[6]
			msgStreamID := uint32(msgHeaderBuf[7]) | uint32(msgHeaderBuf[8])<<8 | uint32(msgHeaderBuf[9])<<16 | uint32(msgHeaderBuf[10])<<24

			chunkStream.initHeader(timestamp, int(msgLen), msgTypeID, StreamID(msgStreamID))

			messageStream := newReceiveMessageStream(StreamID(msgStreamID))

			chunkStream.addMessageStream(messageStream)

			messageHeader = chunkStream.currentHeader()
		case varLenChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:7]); err != nil {
				return err
			}
			timestampDeltaField := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])
			timestampDelta, err := decodeChunkTimestamp(timestampDeltaField, r)
			if err != nil {
				return err
			}
			msgLen := uint32(msgHeaderBuf[3])<<16 | uint32(msgHeaderBuf[4])<<8 | uint32(msgHeaderBuf[5])
			msgTypeID := msgHeaderBuf[6]

			messageHeader = chunkStream.nextHeader(&timestampDelta, &msgLen, &msgTypeID)
		case timeDeltaChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:3]); err != nil {
				return err
			}
			timestampDeltaField := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])
			timestampDelta, err := decodeChunkTimestamp(timestampDeltaField, r)
			if err != nil {
				return err
			}

			messageHeader = chunkStream.nextHeader(&timestampDelta, nil, nil)
		case contChunkType:
			messageHeader = chunkStream.nextHeader(nil, nil, nil)
		default:
			return ErrUnsupportedChunkType
		}

		messageStream := chunkStream.currentMessageStream()
		message := messageStream.nextMessage
		if message == nil {
			message = acquireMessage()
			messageStream.nextMessage = message
		}

		nextChunkSize := min(message.unreadBytes(), int(conn.maxChunkSize))
		if len(payloadBuf) < nextChunkSize {
			payloadBuf = make([]byte, nextChunkSize)
		}
		chunkPayload := payloadBuf[:nextChunkSize]
		if _, err := io.ReadFull(r, chunkPayload); err != nil {
			return err
		}

		err := messageStream.appendChunk(chunkPayload)
		if err != nil {
			return err
		}
	}
}

func (c *Conn) openSendMessageStream(streamID StreamID) *sendMessageStream {
	return newSendMessageStream(streamID)
}

func (c *Conn) OpenStream() (*SendStream, error) {
	return newSendStream(), nil
}

func (c *Conn) AcceptStream() (*ReceiveStream, error) {
	return newReceiveStream(), nil
}

func (c *Conn) LocalAddr() net.Addr {
	return c.transport.LocalAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.transport.RemoteAddr()
}

func (c *Conn) Close() error {
	return c.transport.Close()
}

func newSendStream() *SendStream {
	return &SendStream{
		messageStreams: make(map[StreamID]*sendMessageStream),
	}
}

type SendStream struct {
	streamID       StreamID
	messageStreams map[StreamID]*sendMessageStream
}

func (s *SendStream) StreamID() StreamID {
	return s.streamID
}

func (s *SendStream) messageStream(streamID StreamID) *sendMessageStream {
	if s.messageStreams == nil {
		s.messageStreams = make(map[StreamID]*sendMessageStream)
	}
	if stream, ok := s.messageStreams[streamID]; ok {
		return stream
	}
	stream := newSendMessageStream(streamID)
	s.messageStreams[streamID] = stream
	return stream
}

func newReceiveStream() *ReceiveStream {
	return &ReceiveStream{}
}

type ReceiveStream struct {
	streamID StreamID

	messageChan chan *receiveChunkStream
}

func (s *ReceiveStream) StreamID() StreamID {
	return s.streamID
}

func (s *ReceiveStream) ReadMessage() (*receiveChunkStream, error) {
	msg, ok := <-s.messageChan
	if !ok {
		return nil, io.ErrClosedPipe
	}
	return msg, nil
}
