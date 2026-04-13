package rtmp

import "errors"

var (
	ErrMessageTooLarge      = errors.New("rtmp: message too large")
	ErrInvalidChunkSize     = errors.New("rtmp: invalid chunk size")
	ErrServerRejected       = errors.New("rtmp: server rejected")
	ErrCreateStreamRejected = errors.New("rtmp: createStream rejected")
	ErrUnsupportedFrameType = errors.New("rtmp: unsupported frame type")
)
