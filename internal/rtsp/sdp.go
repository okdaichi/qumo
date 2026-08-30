package rtsp

import (
	"strings"
)

// SDP represents a Session Description Protocol message.
type SDP struct {
	Medias []SDPMedia
}

// SDPMedia represents a media description in SDP.
type SDPMedia struct {
	Type       string // video, audio
	Control    string
	RtpMap     string
	Fmtp       string
	Attributes map[string]string
}

// ParseSDP parses a minimal SDP description.
func ParseSDP(data string) *SDP {
	sdp := &SDP{}
	var currentMedia *SDPMedia

	lines := strings.SplitSeq(data, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		tag := parts[0]
		val := parts[1]

		switch tag {
		case "m":
			mParts := strings.Split(val, " ")
			currentMedia = &SDPMedia{
				Type:       mParts[0],
				Attributes: make(map[string]string),
			}
			sdp.Medias = append(sdp.Medias, *currentMedia)
			currentMedia = &sdp.Medias[len(sdp.Medias)-1]
		case "a":
			if currentMedia != nil {
				aParts := strings.SplitN(val, ":", 2)
				attr := aParts[0]
				if len(aParts) == 2 {
					currentMedia.Attributes[attr] = aParts[1]
					switch attr {
					case "control":
						currentMedia.Control = aParts[1]
					case "rtpmap":
						currentMedia.RtpMap = aParts[1]
					case "fmtp":
						currentMedia.Fmtp = aParts[1]
					}
				} else {
					currentMedia.Attributes[attr] = ""
				}
			}
		}
	}
	return sdp
}
