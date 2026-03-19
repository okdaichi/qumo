package rtmp

import (
	"encoding/binary"
	"io"
)

type MessageTypeID uint8

// Message type IDs for RTMP control messages.
const (
	MessageTypeSetChunkSize     MessageTypeID = 1
	MessageTypeAbort            MessageTypeID = 2
	MessageTypeAck              MessageTypeID = 3
	MessageTypeUserControl      MessageTypeID = 4
	MessageTypeWindowAckSize    MessageTypeID = 5
	MessageTypeSetPeerBandwidth MessageTypeID = 6
)

// Message type IDs for RTMP audio, video, and command messages.
const (
	MessageTypeAudio       MessageTypeID = 8
	MessageTypeVideo       MessageTypeID = 9
	MessageTypeAMF3Data    MessageTypeID = 15
	MessageTypeAMF3Command MessageTypeID = 17
	MessageTypeAMF0Data    MessageTypeID = 18
	MessageTypeAMF0Command MessageTypeID = 20
	MessageTypeAggregate   MessageTypeID = 22
)

type ChunkSize uint32

const DefaultChunkSize ChunkSize = 128

type MessageSetChunkSize struct {
	ChunkSize uint32
}

func (m MessageSetChunkSize) encode(w io.Writer) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, m.ChunkSize&0x7FFFFFFF)
	_, err := w.Write(buf)
	return err
}

func (m *MessageSetChunkSize) decode(r io.Reader) error {
	buf := make([]byte, 4)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.ChunkSize = binary.BigEndian.Uint32(buf) & 0x7FFFFFFF
	return nil
}

type MessageAbort struct {
	ChunkStreamID uint32
}

func (m MessageAbort) encode(w io.Writer) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, m.ChunkStreamID)
	_, err := w.Write(buf)
	return err
}

func (m *MessageAbort) decode(r io.Reader) error {
	buf := make([]byte, 4)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.ChunkStreamID = binary.BigEndian.Uint32(buf)
	return nil
}

type MessageAck struct {
	SequenceNumber uint32
}

func (m MessageAck) encode(w io.Writer) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, m.SequenceNumber)
	_, err := w.Write(buf)
	return err
}

func (m *MessageAck) decode(r io.Reader) error {
	buf := make([]byte, 4)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.SequenceNumber = binary.BigEndian.Uint32(buf)
	return nil
}

type MessageUserControl struct {
	EventType uint16
	EventData []byte
}

func (m MessageUserControl) encode(w io.Writer) error {
	buf := make([]byte, 2+len(m.EventData))
	binary.BigEndian.PutUint16(buf, m.EventType)
	copy(buf[2:], m.EventData)
	_, err := w.Write(buf)
	return err
}

func (m *MessageUserControl) decode(r io.Reader) error {
	buf := make([]byte, 2)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.EventType = binary.BigEndian.Uint16(buf)
	m.EventData = make([]byte, 0) // Event data length is determined by the message length, which should be handled by the caller.
	return nil
}

type MessageWindowAckSize struct {
	Size uint32
}

func (m MessageWindowAckSize) encode(w io.Writer) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, m.Size)
	_, err := w.Write(buf)
	return err
}

func (m *MessageWindowAckSize) decode(r io.Reader) error {
	buf := make([]byte, 4)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.Size = binary.BigEndian.Uint32(buf)
	return nil
}

type MessageSetPeerBandwidth struct {
	Bandwidth uint32
	LimitType uint8
}

func (m MessageSetPeerBandwidth) encode(w io.Writer) error {
	buf := make([]byte, 5)
	binary.BigEndian.PutUint32(buf, m.Bandwidth)
	buf[4] = m.LimitType
	_, err := w.Write(buf)
	return err
}

func (m *MessageSetPeerBandwidth) decode(r io.Reader) error {
	buf := make([]byte, 5)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	m.Bandwidth = binary.BigEndian.Uint32(buf)
	m.LimitType = buf[4]
	return nil
}

type backpointer = uint32

type message interface {
	MessageTypeID() MessageTypeID
}

type MessageAggregate struct {
	Messages map[message]backpointer
}

type BandwidthLimitType uint8

const (
	BandwidthLimitHard    BandwidthLimitType = 0
	BandwidthLimitSoft    BandwidthLimitType = 1
	BandwidthLimitDynamic BandwidthLimitType = 2
)

type MessageMediaData struct {
	TypeID    uint8
	Timestamp uint32
	StreamID  uint32
	Data      []byte
}
