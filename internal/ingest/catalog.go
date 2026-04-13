package ingest

import (
	"encoding/json"
	"strconv"
)

// catalog types mirror the MSF (Media over Streaming Format) catalog
// expected by the web subscriber.

type msfCatalog struct {
	Version int        `json:"version"`
	Tracks  []msfTrack `json:"tracks"`
}

type msfTrack struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Packaging string `json:"packaging"`
	IsLive    bool   `json:"isLive"`
	Codec     string `json:"codec"`

	// Video-only fields.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// Audio-only fields.
	SampleRate    int    `json:"samplerate,omitempty"`
	ChannelConfig string `json:"channelConfig,omitempty"`
}

// buildCatalogJSON builds the MSF catalog JSON payload from the video and
// audio codec configurations extracted from FLV sequence headers.
// Either config may be nil if the corresponding track is not present.
func buildCatalogJSON(video *AVCConfig, audio *AACConfig) ([]byte, error) {
	cat := msfCatalog{Version: 1}

	if video != nil {
		cat.Tracks = append(cat.Tracks, msfTrack{
			Name:      "video",
			Role:      "video",
			Packaging: "loc",
			IsLive:    true,
			Codec:     video.CodecString(),
			Width:     video.Width,
			Height:    video.Height,
		})
	}

	if audio != nil {
		cat.Tracks = append(cat.Tracks, msfTrack{
			Name:          "audio",
			Role:          "audio",
			Packaging:     "loc",
			IsLive:        true,
			Codec:         audio.CodecString(),
			SampleRate:    audio.SampleRate,
			ChannelConfig: strconv.Itoa(audio.ChannelConfig),
		})
	}

	return json.Marshal(cat)
}
