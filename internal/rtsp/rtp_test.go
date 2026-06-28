package rtsp

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalRTP(t *testing.T) {
	tests := map[string]struct {
		data      []byte
		want      *RTPPacket
		wantErr   bool
		errString string
	}{
		"valid packet": {
			data: func() []byte {
				// Create a valid RTP packet
				// Version=2 (10), Padding=1, Extension=1, Marker=1, PayloadType=96 (1100000)
				// Byte 0: 10(Version) 1(Padding) 1(Extension) 0000 (CSRC count) = 1011 0000 = 0xB0
				// Byte 1: 1(Marker) 1100000(PayloadType) = 1110 0000 = 0xE0
				// Bytes 2-3: SeqNumber = 1234 = 0x04D2
				// Bytes 4-7: Timestamp = 5678 = 0x0000162E
				// Bytes 8-11: SSRC = 9012 = 0x00002334
				// Payload: 0xDE, 0xAD, 0xBE, 0xEF
				b := make([]byte, 12+4)
				b[0] = 0xB0
				b[1] = 0xE0
				binary.BigEndian.PutUint16(b[2:4], 1234)
				binary.BigEndian.PutUint32(b[4:8], 5678)
				binary.BigEndian.PutUint32(b[8:12], 9012)
				copy(b[12:], []byte{0xDE, 0xAD, 0xBE, 0xEF})
				return b
			}(),
			want: &RTPPacket{
				Header: RTPHeader{
					Version:        2,
					Padding:        true,
					Extension:      true,
					Marker:         true,
					PayloadType:    96,
					SequenceNumber: 1234,
					Timestamp:      5678,
					SSRC:           9012,
				},
				Payload: []byte{0xDE, 0xAD, 0xBE, 0xEF},
			},
			wantErr: false,
		},
		"valid packet minimal": {
			data: func() []byte {
				// Create a minimal valid RTP packet
				// Version=0, Padding=0, Extension=0, Marker=0, PayloadType=0
				b := make([]byte, 12)
				b[0] = 0x00
				b[1] = 0x00
				binary.BigEndian.PutUint16(b[2:4], 0)
				binary.BigEndian.PutUint32(b[4:8], 0)
				binary.BigEndian.PutUint32(b[8:12], 0)
				return b
			}(),
			want: &RTPPacket{
				Header: RTPHeader{
					Version:        0,
					Padding:        false,
					Extension:      false,
					Marker:         false,
					PayloadType:    0,
					SequenceNumber: 0,
					Timestamp:      0,
					SSRC:           0,
				},
				Payload: []byte{},
			},
			wantErr: false,
		},
		"too short": {
			data:      []byte{0x80, 0x60, 0x00, 0x01}, // only 4 bytes
			want:      nil,
			wantErr:   true,
			errString: "rtp packet too short",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := UnmarshalRTP(tc.data)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.errString, err.Error())
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
