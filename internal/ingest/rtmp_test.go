package ingest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsVideoKeyframe(t *testing.T) {
	tests := map[string]struct {
		data []byte
		want bool
	}{
		"empty data": {
			data: []byte{},
			want: false,
		},
		"standard keyframe (AVC, type=1, codec=7)": {
			// byte 0 = 0x17 → FrameType=1 (keyframe), CodecID=7 (AVC)
			data: []byte{0x17, 0x01, 0x00, 0x00, 0x00},
			want: true,
		},
		"standard inter frame (AVC, type=2, codec=7)": {
			// byte 0 = 0x27 → FrameType=2 (inter), CodecID=7
			data: []byte{0x27, 0x01, 0x00, 0x00, 0x00},
			want: false,
		},
		"enhanced RTMP keyframe": {
			// Enhanced: isExHeader=1, FrameType=1, PacketType varies
			// byte 0 = 0b1_001_xxxx → bits[4:6] = 001 = keyframe
			data: []byte{0x90},
			want: true,
		},
		"enhanced RTMP non-keyframe": {
			// Enhanced: isExHeader=1, FrameType=2
			// byte 0 = 0b1_010_xxxx → bits[4:6] = 010 = inter
			data: []byte{0xA0},
			want: false,
		},
		"standard keyframe H.263 (type=1, codec=2)": {
			// byte 0 = 0x12 → FrameType=1, CodecID=2
			data: []byte{0x12, 0x00},
			want: true,
		},
		"disposable inter frame (type=3)": {
			// byte 0 = 0x37 → FrameType=3 (disposable)
			data: []byte{0x37, 0x01},
			want: false,
		},
		"single byte keyframe": {
			data: []byte{0x17},
			want: true,
		},
		"single byte non-keyframe": {
			data: []byte{0x27},
			want: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, isVideoKeyframe(tt.data))
		})
	}
}
