package rtmp

import "encoding/binary"

type StreamID uint32

func (conn *Conn) openControlStream() (*ControlStream, error) {
	// TODO: Use sync.Once to ensure only one control stream is created
	sender, err := conn.OpenStream()
	if err != nil {
		return nil, err
	}
	receiver, err := conn.AcceptStream()
	if err != nil {
		return nil, err
	}
	stream := &ControlStream{
		sender:          sender,
		receiver:        receiver,
		localChunkSize:  DefaultChunkSize,
		remoteChunkSize: DefaultChunkSize,
	}
	return stream, nil
}

type ControlStream struct {
	sender   *SendStream
	receiver *ReceiveStream

	localChunkSize  ChunkSize
	remoteChunkSize ChunkSize
}

func (s *ControlStream) StreamID() StreamID {
	return messageStreamIDControl
}

func (s *ControlStream) chunkStreamID() uint32 {
	return chunkStreamIDControl
}

func (s *ControlStream) SetChunkSize(size uint32) error {
	s.localChunkSize = ChunkSize(size)
	return nil
}

type EventType uint16

const (
	eventTypeStreamBegin      EventType = 0
	eventTypeStreamEOF        EventType = 1
	eventTypeStreamDry        EventType = 2
	eventTypeSetBufferLength  EventType = 3
	eventTypeStreamIsRecorded EventType = 4
	eventTypePingRequest      EventType = 6
	eventTypePingResponse     EventType = 7
)

func messageStreamBegin(streamID StreamID) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypeStreamBegin),
	}
	binary.BigEndian.PutUint32(m.EventData, uint32(streamID))
	return m
}

func messageStreamEOF(streamID StreamID) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypeStreamEOF),
	}
	binary.BigEndian.PutUint32(m.EventData, uint32(streamID))
	return m
}

func messageStreamDry(streamID StreamID) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypeStreamDry),
	}
	binary.BigEndian.PutUint32(m.EventData, uint32(streamID))
	return m
}

func messageSetBufferLength(streamID StreamID, bufferLength uint32) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypeSetBufferLength),
	}
	binary.BigEndian.PutUint32(m.EventData, uint32(streamID))
	binary.BigEndian.PutUint32(m.EventData[4:], bufferLength)
	return m
}

func messageStreamIsRecorded(streamID StreamID) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypeStreamIsRecorded),
	}
	binary.BigEndian.PutUint32(m.EventData, uint32(streamID))
	return m
}

func messagePingRequest(timestamp uint32) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypePingRequest),
	}
	binary.BigEndian.PutUint32(m.EventData, timestamp)
	return m
}

func messagePingResponse(timestamp uint32) MessageUserControl {
	m := MessageUserControl{
		EventType: uint16(eventTypePingResponse),
	}
	binary.BigEndian.PutUint32(m.EventData, timestamp)
	return m
}
