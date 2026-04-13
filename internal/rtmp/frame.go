package rtmp

import "fmt"

// FrameType distinguishes audio, video, and metadata frames exchanged
// through [MessageReader] and [MessageWriter].
type FrameType uint8

const (
	// FrameTypeAudio identifies an audio frame.
	FrameTypeAudio FrameType = FrameType(MessageTypeAudio) // 8
	// FrameTypeVideo identifies a video frame.
	FrameTypeVideo FrameType = FrameType(MessageTypeVideo) // 9
	// FrameTypeMetadata identifies a metadata frame (AMF0 encoded).
	FrameTypeMetadata FrameType = FrameType(MessageTypeAMF0Data) // 18
)

// Frame carries a single audio, video, or metadata payload exchanged via
// [MessageReader.ReadFrame] or [MessageWriter.WriteFrame].
type Frame struct {
	Type FrameType
	// Timestamp is the presentation timestamp in milliseconds.
	Timestamp uint32
	// Data holds the raw payload bytes. For audio and video frames this
	// contains the codec-specific bitstream. For metadata frames this
	// contains AMF0-encoded key/value pairs.
	Data []byte
}

// String returns a human-readable name for the frame type.
func (ft FrameType) String() string {
	switch ft {
	case FrameTypeAudio:
		return "audio"
	case FrameTypeVideo:
		return "video"
	case FrameTypeMetadata:
		return "metadata"
	default:
		return fmt.Sprintf("FrameType(%d)", uint8(ft))
	}
}
