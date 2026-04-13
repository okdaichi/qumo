package ingest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCatalogJSON_VideoAndAudio(t *testing.T) {
	video := &AVCConfig{
		ProfileIDC:    0x64,
		ProfileCompat: 0x00,
		LevelIDC:      0x1F,
		Width:         1920,
		Height:        1080,
	}
	audio := &AACConfig{
		ObjectType:    2,
		SampleRate:    48000,
		ChannelConfig: 2,
	}

	data, err := buildCatalogJSON(video, audio)
	require.NoError(t, err)

	var cat msfCatalog
	require.NoError(t, json.Unmarshal(data, &cat))

	assert.Equal(t, 1, cat.Version)
	require.Len(t, cat.Tracks, 2)

	vt := cat.Tracks[0]
	assert.Equal(t, "video", vt.Name)
	assert.Equal(t, "video", vt.Role)
	assert.Equal(t, "loc", vt.Packaging)
	assert.True(t, vt.IsLive)
	assert.Equal(t, "avc1.64001f", vt.Codec)
	assert.Equal(t, 1920, vt.Width)
	assert.Equal(t, 1080, vt.Height)

	at := cat.Tracks[1]
	assert.Equal(t, "audio", at.Name)
	assert.Equal(t, "audio", at.Role)
	assert.Equal(t, "loc", at.Packaging)
	assert.True(t, at.IsLive)
	assert.Equal(t, "mp4a.40.2", at.Codec)
	assert.Equal(t, 48000, at.SampleRate)
	assert.Equal(t, "2", at.ChannelConfig)
}

func TestBuildCatalogJSON_VideoOnly(t *testing.T) {
	video := &AVCConfig{
		ProfileIDC: 0x42,
		LevelIDC:   0x1E,
		Width:      1280,
		Height:     720,
	}

	data, err := buildCatalogJSON(video, nil)
	require.NoError(t, err)

	var cat msfCatalog
	require.NoError(t, json.Unmarshal(data, &cat))

	assert.Equal(t, 1, cat.Version)
	require.Len(t, cat.Tracks, 1)
	assert.Equal(t, "video", cat.Tracks[0].Role)
	assert.Equal(t, "avc1.42001e", cat.Tracks[0].Codec)
}

func TestBuildCatalogJSON_AudioOnly(t *testing.T) {
	audio := &AACConfig{
		ObjectType:    2,
		SampleRate:    44100,
		ChannelConfig: 1,
	}

	data, err := buildCatalogJSON(nil, audio)
	require.NoError(t, err)

	var cat msfCatalog
	require.NoError(t, json.Unmarshal(data, &cat))

	require.Len(t, cat.Tracks, 1)
	assert.Equal(t, "audio", cat.Tracks[0].Role)
	assert.Equal(t, "mp4a.40.2", cat.Tracks[0].Codec)
	assert.Equal(t, "1", cat.Tracks[0].ChannelConfig)
}
