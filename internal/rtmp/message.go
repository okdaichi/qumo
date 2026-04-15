package rtmp

import (
	"encoding/binary"
	"io"
)

// messageTypeID identifies the type of an RTMP message.
type messageTypeID uint8

// Message type IDs for RTMP control messages (types 1–6).
const (
	// messageTypeSetChunkSize (1) sets the maximum chunk payload size.
	messageTypeSetChunkSize messageTypeID = 1
	// messageTypeAbort (2) discards a partially received message.
	messageTypeAbort messageTypeID = 2
	// messageTypeAck (3) acknowledges bytes received.
	messageTypeAck messageTypeID = 3
	// messageTypeUserControl (4) carries user control events (ping, stream begin, etc.).
	messageTypeUserControl messageTypeID = 4
	// messageTypeWindowAckSize (5) sets the acknowledgement window size.
	messageTypeWindowAckSize messageTypeID = 5
	// messageTypeSetPeerBandwidth (6) limits the peer’s output bandwidth.
	messageTypeSetPeerBandwidth messageTypeID = 6
)

// Message type IDs for RTMP audio, video, and command messages.
const (
	// messageTypeAudio (8) carries an audio payload.
	messageTypeAudio messageTypeID = 8
	// messageTypeVideo (9) carries a video payload.
	messageTypeVideo messageTypeID = 9
	// messageTypeAMF3Data (15) carries AMF3-encoded metadata.
	messageTypeAMF3Data messageTypeID = 15
	// messageTypeAMF3Command (17) carries an AMF3-encoded command.
	messageTypeAMF3Command messageTypeID = 17
	// messageTypeAMF0Data (18) carries AMF0-encoded metadata.
	messageTypeAMF0Data messageTypeID = 18
	// messageTypeAMF0Command (20) carries an AMF0-encoded command.
	messageTypeAMF0Command messageTypeID = 20
)

// messageSetChunkSize is a protocol control message (type 1) that sets the
// maximum chunk payload size for subsequent chunks.
type messageSetChunkSize struct {
	ChunkSize uint32
}

func (m messageSetChunkSize) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.ChunkSize&0x7FFFFFFF)
	_, err := w.Write(buf[:])
	return err
}

func (m *messageSetChunkSize) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.ChunkSize = binary.BigEndian.Uint32(buf[:]) & 0x7FFFFFFF
	return nil
}

// messageAbort is a protocol control message (type 2) that notifies the peer
// to discard a partially received message on the specified chunk stream.
type messageAbort struct {
	ChunkStreamID uint32
}

func (m messageAbort) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.ChunkStreamID)
	_, err := w.Write(buf[:])
	return err
}

func (m *messageAbort) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.ChunkStreamID = binary.BigEndian.Uint32(buf[:])
	return nil
}

// messageAck is a protocol control message (type 3) that acknowledges the
// total number of bytes received so far.
type messageAck struct {
	SequenceNumber uint32
}

func (m messageAck) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.SequenceNumber)
	_, err := w.Write(buf[:])
	return err
}

func (m *messageAck) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.SequenceNumber = binary.BigEndian.Uint32(buf[:])
	return nil
}

// messageUserControl is a protocol control message (type 4) that carries
// user control events such as stream begin, ping request, and ping response.
type messageUserControl struct {
	EventType uint16
	EventData []byte
}

func (m messageUserControl) encode(w io.Writer) error {
	buf := make([]byte, 2+len(m.EventData))
	binary.BigEndian.PutUint16(buf, m.EventType)
	copy(buf[2:], m.EventData)
	_, err := w.Write(buf)
	return err
}

func (m *messageUserControl) decode(r io.Reader) error {
	var buf [2]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.EventType = binary.BigEndian.Uint16(buf[:])
	m.EventData, err = io.ReadAll(r)
	return err
}

// messageWindowAckSize is a protocol control message (type 5) that sets the
// window size for acknowledgement. The peer MUST send an [messageAck] after
// receiving the indicated number of bytes.
type messageWindowAckSize struct {
	Size uint32
}

func (m messageWindowAckSize) encode(w io.Writer) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], m.Size)
	_, err := w.Write(buf[:])
	return err
}

func (m *messageWindowAckSize) decode(r io.Reader) error {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.Size = binary.BigEndian.Uint32(buf[:])
	return nil
}

// messageSetPeerBandwidth is a protocol control message (type 6) that limits
// the output bandwidth of the receiving peer.
type messageSetPeerBandwidth struct {
	Bandwidth uint32
	LimitType bandwidthLimitType
}

func (m messageSetPeerBandwidth) encode(w io.Writer) error {
	var buf [5]byte
	binary.BigEndian.PutUint32(buf[:], m.Bandwidth)
	buf[4] = uint8(m.LimitType)
	_, err := w.Write(buf[:])
	return err
}

func (m *messageSetPeerBandwidth) decode(r io.Reader) error {
	var buf [5]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return err
	}
	m.Bandwidth = binary.BigEndian.Uint32(buf[:])
	m.LimitType = bandwidthLimitType(buf[4])
	return nil
}

// bandwidthLimitType specifies how the peer should limit its output bandwidth.
type bandwidthLimitType uint8

const (
	// bandwidthLimitHard requires the peer to limit its output bandwidth to
	// the indicated window size.
	bandwidthLimitHard bandwidthLimitType = 0
	// bandwidthLimitSoft requests the peer to limit its output bandwidth to
	// the indicated window size, or the already effective limit, whichever
	// is smaller.
	bandwidthLimitSoft bandwidthLimitType = 1
	// bandwidthLimitDynamic treats the limit as Hard if the previous limit
	// type was Hard, and as Soft otherwise.
	bandwidthLimitDynamic bandwidthLimitType = 2
)
