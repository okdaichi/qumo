package rtsp

import (
	"encoding/binary"
	"fmt"
)

// RTPPacket represents an RTP packet.
type RTPPacket struct {
	Header  RTPHeader
	Payload []byte
}

// RTPHeader represents the fixed header of an RTP packet.
type RTPHeader struct {
	Version        uint8
	Padding        bool
	Extension      bool
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
}

// UnmarshalRTP parses an RTP packet from data.
//
// It skips the CSRC list and the header extension (RFC 3550 §5.1) and strips
// trailing padding, so Payload is exactly the media payload — required for real
// RTSP sources (e.g. IP cameras) that set the CSRC count, the extension bit, or
// padding. The previous implementation returned data[12:] unconditionally, which
// left CSRC/extension/padding bytes in front of / after the payload and corrupted
// depacketization for any source using them.
func UnmarshalRTP(data []byte) (*RTPPacket, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("rtp packet too short")
	}

	h := RTPHeader{
		Version:        data[0] >> 6,
		Padding:        (data[0]>>5)&1 == 1,
		Extension:      (data[0]>>4)&1 == 1,
		Marker:         (data[1]>>7)&1 == 1,
		PayloadType:    data[1] & 0x7F,
		SequenceNumber: binary.BigEndian.Uint16(data[2:4]),
		Timestamp:      binary.BigEndian.Uint32(data[4:8]),
		SSRC:           binary.BigEndian.Uint32(data[8:12]),
	}

	// Payload starts after the fixed header (12 bytes), the CSRC list
	// (CC contributing-source identifiers, 4 bytes each), and — if the
	// extension bit is set — a 4-byte extension header plus its body.
	off := 12 + int(data[0]&0x0F)*4 // CSRC
	if off > len(data) {
		return nil, fmt.Errorf("rtp packet shorter than CSRC list")
	}
	if h.Extension {
		if off+4 > len(data) {
			return nil, fmt.Errorf("rtp packet shorter than extension header")
		}
		// Extension header: profile (2 bytes, ignored) + length (2 bytes,
		// 32-bit words of extension body).
		extLen := int(binary.BigEndian.Uint16(data[off+2:])) * 4
		off += 4 + extLen
		if off > len(data) {
			return nil, fmt.Errorf("rtp packet shorter than extension body")
		}
	}

	payload := data[off:]

	// Padding: the last byte of the packet gives the number of padding bytes
	// (including itself) appended after the payload.
	if h.Padding {
		if len(payload) == 0 {
			return nil, fmt.Errorf("rtp padding flag set but no trailing byte")
		}
		padLen := int(payload[len(payload)-1])
		if padLen == 0 || padLen > len(payload) {
			return nil, fmt.Errorf("rtp padding length %d out of range", padLen)
		}
		payload = payload[:len(payload)-padLen]
	}

	return &RTPPacket{
		Header:  h,
		Payload: payload,
	}, nil
}
