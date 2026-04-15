package rtmp

// RTMP command message names.
const (
	commandMessageNameConnect      = "connect"
	commandMessageNameCreateStream = "createStream"
	commandMessageNamePublish      = "publish"

	// Professional/common encoder publish commands.
	commandMessageNameReleaseStream = "releaseStream"
	commandMessageNameFCPublish     = "FCPublish"
	commandMessageNameFCUnpublish   = "FCUnpublish"
	commandMessageNameDeleteStream  = "deleteStream"

	// Server responses.
	commandMessageNameResult   = "_result"
	commandMessageNameError    = "_error"
	commandMessageNameOnStatus = "onStatus"
)

// Audio codec capability flags used in the connect command object.
const (
	audioCodecFlagMP3 = 0x0004
	audioCodecFlagAAC = 0x0400
)

// Video codec capability flags used in the connect command object.
const (
	videoCodecFlagH264 = 0x0080
)
