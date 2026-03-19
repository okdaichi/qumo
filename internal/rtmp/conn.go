package rtmp

import (
	"bytes"
	"errors"
	"io"
	"net"
)

func NewConn(transport net.Conn) *Conn {
	return &Conn{
		transport:    transport,
		maxChunkSize: DefaultChunkSize,
	}
}

type Conn struct {
	transport net.Conn

	receivedMessages map[chunkStreamID]*messageDecoder

	maxChunkSize ChunkSize
}

var ErrUnsupportedChunkType = errors.New("unsupported chunk type")
var ErrInvalidChunkHeader = errors.New("invalid chunk header")

func (conn *Conn) readMessage() (*messageDecoder, error) {
	header := chunkBasicHeader{}
	r := conn.transport

	msgHeaderBuf := make([]byte, 11)
	payloadBuf := make([]byte, 0)

	for {
		if err := header.decode(r); err != nil {
			return nil, err
		}

		var decoder *messageDecoder
		var ok bool
		switch chunkType(header.fmt) {
		case initChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:11]); err != nil {
				return nil, err
			}
			timestamp := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])
			msgLen := uint32(msgHeaderBuf[3])<<16 | uint32(msgHeaderBuf[4])<<8 | uint32(msgHeaderBuf[5])
			msgTypeID := msgHeaderBuf[6]
			msgStreamID := uint32(msgHeaderBuf[7]) | uint32(msgHeaderBuf[8])<<8 | uint32(msgHeaderBuf[9])<<16 | uint32(msgHeaderBuf[10])<<24

			decoder, ok = conn.receivedMessages[header.chunkStreamID]
			if ok && !decoder.completed() {
				// This means we have an incomplete message for this chunk stream ID, which is an error according to the RTMP spec.
				return nil, ErrInvalidChunkHeader // TODO: define a more specific error for this case
			}

			decoder = &messageDecoder{ // TODO: Use newMessageDecoder() ?
				chunkStreamID: header.chunkStreamID,
				init: chunkStreamInit{
					timestamp:       timestamp,
					messageLen:      msgLen,
					messageTypeID:   msgTypeID,
					messageStreamID: msgStreamID,
				},
				latestTimestamp: timestamp,
				payload:         bytes.NewBuffer(make([]byte, 0, msgLen)),
			}

			conn.receivedMessages[header.chunkStreamID] = decoder
			// TODO: consider using a sync.Pool for messageDecoders to avoid allocations
		case varLenChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:7]); err != nil {
				return nil, err
			}
			timestampDelta := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])
			msgLen := uint32(msgHeaderBuf[3])<<16 | uint32(msgHeaderBuf[4])<<8 | uint32(msgHeaderBuf[5])
			msgTypeID := msgHeaderBuf[6]

			decoder, ok = conn.receivedMessages[header.chunkStreamID]
			if !ok || decoder.completed() {
				return nil, ErrInvalidChunkHeader
			}
			decoder.init.messageLen = msgLen
			decoder.init.messageTypeID = msgTypeID
			decoder.timeDelta = timestampDelta
			decoder.latestTimestamp += timestampDelta
		case timeDeltaChunkType:
			if _, err := io.ReadFull(r, msgHeaderBuf[:3]); err != nil {
				return nil, err
			}
			timestampDelta := uint32(msgHeaderBuf[0])<<16 | uint32(msgHeaderBuf[1])<<8 | uint32(msgHeaderBuf[2])

			decoder, ok = conn.receivedMessages[header.chunkStreamID]
			if !ok || decoder.completed() {
				return nil, ErrInvalidChunkHeader
			}

			decoder.timeDelta = timestampDelta
			decoder.latestTimestamp += timestampDelta
		case contChunkType:
			// No message header
			decoder, ok = conn.receivedMessages[header.chunkStreamID]
			if !ok || decoder.completed() {
				return nil, ErrInvalidChunkHeader
			}
		default:
			return nil, ErrUnsupportedChunkType
		}

		if decoder.completed() {
			return decoder, nil
		}

		nextChunkSize := min(decoder.unreadBytes(), int(conn.maxChunkSize))
		if len(payloadBuf) < nextChunkSize {
			payloadBuf = make([]byte, nextChunkSize)
		}
		if _, err := io.ReadFull(r, payloadBuf); err != nil {
			return nil, err
		}

		err := decoder.appendChunk(payloadBuf)
		if err != nil {
			return nil, err
		}
	}
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
	return &SendStream{}
}

type SendStream struct {
	streamID StreamID
}

func (s *SendStream) StreamID() StreamID {
	return s.streamID
}

func newReceiveStream() *ReceiveStream {
	return &ReceiveStream{}
}

type ReceiveStream struct {
	streamID StreamID

	messageChan chan *messageDecoder
}

func (s *ReceiveStream) StreamID() StreamID {
	return s.streamID
}

func (s *ReceiveStream) ReadMessage() (*messageDecoder, error) {
	msg, ok := <-s.messageChan
	if !ok {
		return nil, io.ErrClosedPipe
	}
	return msg, nil
}
