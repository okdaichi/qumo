package rtmp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "ErrMessageTooLarge",
			err:      ErrMessageTooLarge,
			expected: "rtmp: message too large",
		},
		{
			name:     "ErrInvalidChunkSize",
			err:      ErrInvalidChunkSize,
			expected: "rtmp: invalid chunk size",
		},
		{
			name:     "ErrServerRejected",
			err:      ErrServerRejected,
			expected: "rtmp: server rejected",
		},
		{
			name:     "ErrCreateStreamRejected",
			err:      ErrCreateStreamRejected,
			expected: "rtmp: createStream rejected",
		},
		{
			name:     "ErrUnsupportedFrameType",
			err:      ErrUnsupportedFrameType,
			expected: "rtmp: unsupported frame type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, tt.err, tt.expected)
		})
	}
}
