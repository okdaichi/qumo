package rtmp

import (
	"encoding/binary"
	"io"
)

// MessageTypeID identifies the type of an RTMP message.
type MessageTypeID uint8

// Message type IDs for RTMP control messages (types 1–6).
const (
	// MessageTypeSetChunkSize (1) sets the maximum chunk payload size.
	MessageTypeSetChunkSize MessageTypeID = 1
	// MessageTypeAbort (2) discards a partially received message.
	MessageTypeAbort MessageTypeID = 2
	// MessageTypeAck (3) acknowledges bytes received.
	MessageTypeAck MessageTypeID = 3
	// MessageTypeUserControl (4) carries user control events (ping, stream begin, etc.).
	MessageTypeUserControl MessageTypeID = 4
	// MessageTypeWindowAckSize (5) sets the acknowledgement window size.
	MessageTypeWindowAckSize MessageTypeID = 5
	// MessageTypeSetPeerBandwidth (6) limits the peer’s output bandwidth.
	MessageTypeSetPeerBandwidth MessageTypeID = 6
)

// Message type IDs for RTMP audio, video, and command messages.
const (
	// MessageTypeAudio (8) carries an audio payload.
	MessageTypeAudio MessageTypeID = 8
	// MessageTypeVideo (9) carries a video payload.
	MessageTypeVideo MessageTypeID = 9
	// MessageTypeAMF3Data (15) carries AMF3-encoded metadata.
	MessageTypeAMF3Data MessageTypeID = 15
	// MessageTypeAMF3Command (17) carries an AMF3-encoded command.
	MessageTypeAMF3Command MessageTypeID = 17
	// MessageTypeAMF0Data (18) carries AMF0-encoded metadata.
	MessageTypeAMF0Data MessageTypeID = 18
	// MessageTypeAMF0Command (20) carries an AMF0-encoded command.
	MessageTypeAMF0Command MessageTypeID = 20
)

// MessageSetChunkSize is a protocol control message (type 1) that sets the
// maximum chunk payload size for subsequent chunks.
type MessageSetChunkSize struct {
	ChunkSize uint32
}

func (m MessageSetChunkSize) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.ChunkSize&0x7FFFFFFF)
	_, err := w.Write(buf[:])
	return err
}

func (m *MessageSetChunkSize) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.ChunkSize = binary.BigEndian.Uint32(buf[:]) & 0x7FFFFFFF
	return nil
}

// MessageAbort is a protocol control message (type 2) that notifies the peer
// to discard a partially received message on the specified chunk stream.
type MessageAbort struct {
	ChunkStreamID uint32
}

func (m MessageAbort) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.ChunkStreamID)
	_, err := w.Write(buf[:])
	return err
}

func (m *MessageAbort) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.ChunkStreamID = binary.BigEndian.Uint32(buf[:])
	return nil
}

// MessageAck is a protocol control message (type 3) that acknowledges the
// total number of bytes received so far.
type MessageAck struct {
	SequenceNumber uint32
}

func (m MessageAck) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.SequenceNumber)
	_, err := w.Write(buf[:])
	return err
}

func (m *MessageAck) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.SequenceNumber = binary.BigEndian.Uint32(buf[:])
	return nil
}

// MessageUserControl is a protocol control message (type 4) that carries
// user control events such as stream begin, ping request, and ping response.
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
	var buf [2]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.EventType = binary.BigEndian.Uint16(buf[:])
	m.EventData, err = io.ReadAll(r)
	return err
}

// MessageWindowAckSize is a protocol control message (type 5) that sets the
// window size for acknowledgement. The peer MUST send an [MessageAck] after
// receiving the indicated number of bytes.
type MessageWindowAckSize struct {
	Size uint32
}

func (m MessageWindowAckSize) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.Size)
	_, err := w.Write(buf[:])
	return err
}

func (m *MessageWindowAckSize) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.Size = binary.BigEndian.Uint32(buf[:])
	return nil
}

// MessageSetPeerBandwidth is a protocol control message (type 6) that limits
// the output bandwidth of the receiving peer.
type MessageSetPeerBandwidth struct {
	Bandwidth uint32
	LimitType BandwidthLimitType
}

func (m MessageSetPeerBandwidth) encode(w io.Writer) error {
	var buf [5]byte
	binary.BigEndian.PutUint32(buf[:], m.Bandwidth)
	buf[4] = uint8(m.LimitType)
	_, err := w.Write(buf[:])
	return err
}

func (m *MessageSetPeerBandwidth) decode(r io.Reader) error {
	var buf [5]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.Bandwidth = binary.BigEndian.Uint32(buf[:])
	m.LimitType = BandwidthLimitType(buf[4])
	return nil
}

// BandwidthLimitType specifies how the peer should limit its output bandwidth.
type BandwidthLimitType uint8

const (
	// BandwidthLimitHard requires the peer to limit its output bandwidth to
	// the indicated window size.
	BandwidthLimitHard BandwidthLimitType = 0
	// BandwidthLimitSoft requests the peer to limit its output bandwidth to
	// the indicated window size, or the already effective limit, whichever
	// is smaller.
	BandwidthLimitSoft BandwidthLimitType = 1
	// BandwidthLimitDynamic treats the limit as Hard if the previous limit
	// type was Hard, and as Soft otherwise.
	BandwidthLimitDynamic BandwidthLimitType = 2
)
