package rtmp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFrameType_String(t *testing.T) {
	tests := map[string]struct {
		ft       FrameType
		expected string
	}{
		"audio":    {ft: FrameTypeAudio, expected: "audio"},
		"video":    {ft: FrameTypeVideo, expected: "video"},
		"metadata": {ft: FrameTypeMetadata, expected: "metadata"},
		"unknown":  {ft: FrameType(99), expected: "FrameType(99)"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ft.String())
		})
	}
}

func TestFrameType_Constants(t *testing.T) {
	// Verify that FrameType constants map to the correct messageTypeID values.
	assert.Equal(t, FrameType(messageTypeAudio), FrameTypeAudio)
	assert.Equal(t, FrameType(messageTypeVideo), FrameTypeVideo)
	assert.Equal(t, FrameType(messageTypeAMF0Data), FrameTypeMetadata)
}
