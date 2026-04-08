package rtmp

import (
	"errors"
)

type messageHeaderManager struct {
	latestHeader messageHeader
	// latestTimestamp uint32
	// latestLength    int
	// latestTypeID    uint8
	// latestStreamID  StreamID
	timeDelta uint32
}

func (m *messageHeaderManager) init(timestamp uint32, length int, typeID uint8, streamID StreamID) {
	m.latestHeader = messageHeader{
		timestamp:       timestamp,
		messageLength:   length,
		messageTypeID:   typeID,
		messageStreamID: streamID,
	}
}

func (m *messageHeaderManager) timestampDiff(newTimestamp uint32) uint32 {
	if newTimestamp >= m.latestHeader.timestamp {
		return newTimestamp - m.latestHeader.timestamp
	}
	return newTimestamp - m.latestHeader.timestamp
}

func (m *messageHeaderManager) nextHeader(timeDelta *uint32, messageLen *uint32, typeID *uint8) messageHeader {
	if timeDelta != nil {
		m.timeDelta = *timeDelta
	}
	if messageLen != nil && typeID != nil {
		m.latestHeader.messageLength = int(*messageLen)
		m.latestHeader.messageTypeID = *typeID
	} else {
		panic("invalid header parameters for chunk type 1 or 2")
	}
	return messageHeader{
		timestamp:       m.latestHeader.timestamp + m.timeDelta,
		messageLength:   m.latestHeader.messageLength,
		messageTypeID:   m.latestHeader.messageTypeID,
		messageStreamID: m.latestHeader.messageStreamID,
	}
}

func (m *messageHeaderManager) currentHeader() messageHeader {
	return m.latestHeader
}

type receiveChunkStream struct {
	chunkStreamID chunkStreamID
	headerManager messageHeaderManager

	messageStreams map[StreamID]*receiveMessageStream
}

func newReceiveChunkStream(chunkStreamID chunkStreamID) *receiveChunkStream {
	return &receiveChunkStream{
		chunkStreamID:  chunkStreamID,
		headerManager:  messageHeaderManager{},
		messageStreams: make(map[StreamID]*receiveMessageStream),
	}
}

var ErrMessageTooLong = errors.New("message too long")

func (s *receiveChunkStream) addMessageStream(msgStream *receiveMessageStream) {
	s.messageStreams[msgStream.streamID] = msgStream
}

func (s *receiveChunkStream) currentMessageStream() *receiveMessageStream {
	return s.messageStreams[s.headerManager.currentHeader().messageStreamID]
}

func (s *receiveChunkStream) initHeader(timestamp uint32, length int, typeID uint8, streamID StreamID) {
	s.headerManager.init(timestamp, length, typeID, streamID)
}

func (s *receiveChunkStream) nextHeader(timeDelta *uint32, messageLen *uint32, typeID *uint8) messageHeader {
	return s.headerManager.nextHeader(timeDelta, messageLen, typeID)
}

func (s *receiveChunkStream) currentHeader() messageHeader {
	return s.headerManager.currentHeader()
}

func (s *receiveMessageStream) appendChunk(chunk []byte) error {
	if s.nextMessage == nil {
		s.nextMessage = acquireMessage()
	}
	message := s.nextMessage

	_, err := message.payload.Write(chunk)
	if err != nil {
		return err
	}

	if message.unreadBytes() == 0 {
		s.enqueueMessage()
	}

	return nil
}

type receiveMessageStream struct {
	streamID    StreamID
	messages    *messageRingBuffer
	nextMessage *message
}

func newReceiveMessageStream(streamID StreamID) *receiveMessageStream {
	messageStream := &receiveMessageStream{
		streamID: streamID,
		messages: newMessageRingBuffer(DefaultMessageBufferSize),
	}

	return messageStream
}

func (s *receiveMessageStream) enqueueMessage() bool {
	if s == nil {
		return false
	}
	if s.messages == nil {
		s.messages = newMessageRingBuffer(DefaultMessageBufferSize)
	}
	message := s.nextMessage
	s.nextMessage = nil
	return s.messages.Push(message)
}

// func (s *receiveMessageStream) Dequeue() (*message, bool) {
// 	if s == nil || s.messages == nil {
// 		return nil, false
// 	}
// 	return s.messages.Pop()
// }

// func (s *receiveMessageStream) Reset() {
// 	if s == nil || s.messages == nil {
// 		return
// 	}
// 	s.messages.Reset()
// }
