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

	payload := data[12:]
	// Simplification: ignore CSRC and Header Extension for now.

	return &RTPPacket{
		Header:  h,
		Payload: payload,
	}, nil
}
