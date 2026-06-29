package ingest

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildAVCSeqHeader constructs a minimal FLV video sequence header tag.
func buildAVCSeqHeader(profileIDC, profileCompat, levelIDC byte, sps, pps []byte) []byte {
	// FLV tag header: keyframe(1) | AVC(7) = 0x17, AVCPacketType=0, CTO=0
	header := []byte{0x17, 0x00, 0x00, 0x00, 0x00}

	// AVCDecoderConfigurationRecord
	rec := []byte{
		0x01,          // configurationVersion
		profileIDC,    // AVCProfileIndication
		profileCompat, // profile_compatibility
		levelIDC,      // AVCLevelIndication
		0xFF,          // lengthSizeMinusOne = 3 (4 bytes NALU length)
	}

	// numSPS = 1
	rec = append(rec, 0xE1) // reserved(3) | numSPS(5) = 0b111_00001
	spsLen := make([]byte, 2)
	binary.BigEndian.PutUint16(spsLen, uint16(len(sps)))
	rec = append(rec, spsLen...)
	rec = append(rec, sps...)

	// numPPS = 1
	rec = append(rec, 0x01)
	ppsLen := make([]byte, 2)
	binary.BigEndian.PutUint16(ppsLen, uint16(len(pps)))
	rec = append(rec, ppsLen...)
	rec = append(rec, pps...)

	return append(header, rec...)
}

// buildAVCNALUTag constructs an FLV video NALU tag with one AVCC NALU.
func buildAVCNALUTag(frameType byte, cts int32, nalu []byte) []byte {
	tag := []byte{
		(frameType << 4) | 0x07, // FrameType | CodecID=7
		0x01,                    // AVCPacketType = NALU
		byte(cts >> 16),         // CTO high
		byte(cts >> 8),          // CTO mid
		byte(cts),               // CTO low
	}
	// AVCC: 4-byte length prefix + NALU data
	naluLen := make([]byte, 4)
	binary.BigEndian.PutUint32(naluLen, uint32(len(nalu)))
	tag = append(tag, naluLen...)
	tag = append(tag, nalu...)
	return tag
}

func TestParseAVCConfig(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x50}
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB}

	data := buildAVCSeqHeader(0x64, 0x00, 0x1F, sps, pps)

	cfg, err := ParseAVCConfig(data)
	require.NoError(t, err)

	assert.Equal(t, byte(0x64), cfg.ProfileIDC)
	assert.Equal(t, byte(0x00), cfg.ProfileCompat)
	assert.Equal(t, byte(0x1F), cfg.LevelIDC)
	assert.Equal(t, 4, cfg.NALULenSize)
	assert.Equal(t, "avc1.64001f", cfg.CodecString())
	require.Len(t, cfg.SPS, 1)
	assert.Equal(t, sps, cfg.SPS[0])
	require.Len(t, cfg.PPS, 1)
	assert.Equal(t, pps, cfg.PPS[0])
}

func TestParseAVCConfig_Errors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		_, err := ParseAVCConfig([]byte{0x17, 0x00})
		assert.Error(t, err)
	})

	t.Run("not AVC", func(t *testing.T) {
		// CodecID = 4 (not 7)
		_, err := ParseAVCConfig([]byte{0x14, 0x00, 0x00, 0x00, 0x00, 0x01})
		assert.ErrorIs(t, err, ErrNotAVC)
	})

	t.Run("not sequence header", func(t *testing.T) {
		_, err := ParseAVCConfig([]byte{0x17, 0x01, 0x00, 0x00, 0x00, 0x01})
		assert.Error(t, err)
	})
}

func TestParseFLVVideoCTS(t *testing.T) {
	// buildAVCNALUTag encodes CTS as the SI24 at bytes 2-4 of the FLV tag.
	tests := map[string]struct {
		frameType byte
		cts       int32
		nalu      []byte
	}{
		"keyframe zero CTS":   {1, 0, []byte{0x65, 0xAA, 0xBB}},
		"interframe positive": {2, 33, []byte{0x41, 0x01, 0x02}},
		"negative CTS -1":     {2, -1, []byte{0x41}}, // SI24 = 0xFFFFFF
		"large positive CTS":  {2, 1000, []byte{0x41}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			data := buildAVCNALUTag(tt.frameType, tt.cts, tt.nalu)
			assert.Equal(t, tt.cts, parseFLVVideoCTS(data))
		})
	}
}

func TestParseAACConfig(t *testing.T) {
	// FLV audio tag: SoundFormat=10(AAC), rate=3(44100), size=1(16bit), type=1(stereo)
	// AACPacketType=0 (AudioSpecificConfig)
	// ASC: objectType=2(AAC-LC), freqIdx=4(44100), chanCfg=2(stereo)
	// Bits: 00010 0100 0010 ...
	//   byte0 = 0001_0010 = 0x12
	//   byte1 = 0001_0xxx = 0x10
	asc := []byte{0x12, 0x10}
	data := []byte{0xAF, 0x00} // SoundFormat=10|Rate=3|Size=1|Type=1, AACPacketType=0
	data = append(data, asc...)

	cfg, err := ParseAACConfig(data)
	require.NoError(t, err)

	assert.Equal(t, byte(2), cfg.ObjectType)
	assert.Equal(t, 44100, cfg.SampleRate)
	assert.Equal(t, 2, cfg.ChannelConfig)
	assert.Equal(t, "mp4a.40.2", cfg.CodecString())
}

func TestParseAACConfig_Errors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		_, err := ParseAACConfig([]byte{0xAF})
		assert.Error(t, err)
	})

	t.Run("not AAC", func(t *testing.T) {
		// SoundFormat = 2 (MP3)
		_, err := ParseAACConfig([]byte{0x2F, 0x00, 0x12, 0x10})
		assert.ErrorIs(t, err, ErrNotAAC)
	})
}

func TestStripFLVAudioHeader(t *testing.T) {
	rawAAC := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := append([]byte{0xAF, 0x01}, rawAAC...) // AAC raw

	got, err := StripFLVAudioHeader(data)
	require.NoError(t, err)
	assert.Equal(t, rawAAC, got)
}

func TestStripFLVAudioHeader_SeqHeader(t *testing.T) {
	// Should reject sequence headers (type 0).
	_, err := StripFLVAudioHeader([]byte{0xAF, 0x00, 0x12, 0x10})
	assert.Error(t, err)
}

func TestIsVideoSequenceHeader(t *testing.T) {
	assert.True(t, IsVideoSequenceHeader([]byte{0x17, 0x00}))
	assert.False(t, IsVideoSequenceHeader([]byte{0x17, 0x01}))
	assert.True(t, IsVideoSequenceHeader([]byte{0x27, 0x00}))  // not keyframe but codec is AVC + seq header
	assert.False(t, IsVideoSequenceHeader([]byte{0x14, 0x00})) // not AVC
	assert.False(t, IsVideoSequenceHeader([]byte{}))
}

func TestIsAudioSequenceHeader(t *testing.T) {
	assert.True(t, IsAudioSequenceHeader([]byte{0xAF, 0x00}))
	assert.False(t, IsAudioSequenceHeader([]byte{0xAF, 0x01}))
	assert.False(t, IsAudioSequenceHeader([]byte{0x2F, 0x00})) // not AAC
	assert.False(t, IsAudioSequenceHeader([]byte{}))
}

// recordFromSeqHeader returns just the AVCDecoderConfigurationRecord portion of
// a full FLV video sequence-header tag (strips the 5-byte FLV tag header),
// reusing the existing buildAVCSeqHeader fixture as a known-good golden source.
func recordFromSeqHeader(profileIDC, profileCompat, levelIDC byte, sps, pps []byte) []byte {
	return buildAVCSeqHeader(profileIDC, profileCompat, levelIDC, sps, pps)[5:]
}

// TestBuildAVCDecoderConfigurationRecord_Golden asserts the builder emits the
// exact bytes a real FLV sequence header carries — byte-identical to the record
// an RTMP publisher (OBS/ffmpeg) puts on the wire, which is the same shape the
// browser-publish path base64-encodes as initData.
func TestBuildAVCDecoderConfigurationRecord_Golden(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x50}
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB}
	cfg := &AVCConfig{
		ProfileIDC: 0x64, ProfileCompat: 0x00, LevelIDC: 0x1F,
		NALULenSize: 4,
		SPS:         [][]byte{sps},
		PPS:         [][]byte{pps},
	}
	got, err := BuildAVCDecoderConfigurationRecord(cfg)
	require.NoError(t, err)
	want := recordFromSeqHeader(0x64, 0x00, 0x1F, sps, pps)
	assert.Equal(t, want, got)
}

// TestBuildAVCDecoderConfigurationRecord_RoundTrip asserts the builder is the
// exact inverse of parseAVCDecoderConfigurationRecord across multiple SPS/PPS.
func TestBuildAVCDecoderConfigurationRecord_RoundTrip(t *testing.T) {
	orig := &AVCConfig{
		ProfileIDC: 0x64, ProfileCompat: 0x00, LevelIDC: 0x1F,
		NALULenSize: 4,
		SPS:         [][]byte{{0x67, 0x64, 0x00, 0x1F, 0xAC}, {0x67, 0x4D, 0x00}},
		PPS:         [][]byte{{0x68, 0xEB, 0xE3}, {0x68, 0xEE, 0x06, 0xF2}},
	}
	rec, err := BuildAVCDecoderConfigurationRecord(orig)
	require.NoError(t, err)

	parsed, err := parseAVCDecoderConfigurationRecord(rec)
	require.NoError(t, err)
	assert.Equal(t, orig.ProfileIDC, parsed.ProfileIDC)
	assert.Equal(t, orig.ProfileCompat, parsed.ProfileCompat)
	assert.Equal(t, orig.LevelIDC, parsed.LevelIDC)
	assert.Equal(t, orig.NALULenSize, parsed.NALULenSize)
	assert.Equal(t, orig.SPS, parsed.SPS)
	assert.Equal(t, orig.PPS, parsed.PPS)
}

func TestBuildAVCDecoderConfigurationRecord_LengthSize(t *testing.T) {
	// byte[4] = 0xFC (reserved 0b111111) | (NALULenSize-1).
	tests := map[int]byte{1: 0xFC, 2: 0xFD, 3: 0xFE, 4: 0xFF}
	for naluLen, wantFlag := range tests {
		t.Run(fmt.Sprintf("len=%d", naluLen), func(t *testing.T) {
			cfg := &AVCConfig{
				ProfileIDC: 0x64, ProfileCompat: 0x00, LevelIDC: 0x1F,
				NALULenSize: naluLen,
				SPS:         [][]byte{{0x67}},
				PPS:         [][]byte{{0x68}},
			}
			rec, err := BuildAVCDecoderConfigurationRecord(cfg)
			require.NoError(t, err)
			require.Greater(t, len(rec), 4)
			assert.Equal(t, wantFlag, rec[4], "lengthSizeMinusOne byte for NALULenSize=%d", naluLen)
		})
	}
}

func TestBuildAVCDecoderConfigurationRecord_ProfileLevelPassthrough(t *testing.T) {
	cfg := &AVCConfig{
		ProfileIDC: 0x42, ProfileCompat: 0xC0, LevelIDC: 0x1E,
		NALULenSize: 4,
		SPS:         [][]byte{{0x67}},
		PPS:         [][]byte{{0x68}},
	}
	rec, err := BuildAVCDecoderConfigurationRecord(cfg)
	require.NoError(t, err)
	assert.Equal(t, byte(0x01), rec[0], "configurationVersion")
	assert.Equal(t, byte(0x42), rec[1], "AVCProfileIndication")
	assert.Equal(t, byte(0xC0), rec[2], "profile_compatibility")
	assert.Equal(t, byte(0x1E), rec[3], "AVCLevelIndication")
}

func TestBuildAVCDecoderConfigurationRecord_Errors(t *testing.T) {
	valid := &AVCConfig{
		ProfileIDC: 0x64, NALULenSize: 4,
		SPS: [][]byte{{0x67}}, PPS: [][]byte{{0x68}},
	}

	t.Run("nil config", func(t *testing.T) {
		_, err := BuildAVCDecoderConfigurationRecord(nil)
		assert.ErrorIs(t, err, ErrBadAVCConfig)
	})
	t.Run("no SPS", func(t *testing.T) {
		cfg := *valid
		cfg.SPS = nil
		_, err := BuildAVCDecoderConfigurationRecord(&cfg)
		assert.ErrorIs(t, err, ErrBadAVCConfig)
	})
	t.Run("too many SPS", func(t *testing.T) {
		cfg := *valid
		cfg.SPS = make([][]byte, 32) // 5-bit field max is 31
		_, err := BuildAVCDecoderConfigurationRecord(&cfg)
		assert.ErrorIs(t, err, ErrBadAVCConfig)
	})
	t.Run("NALULenSize out of range", func(t *testing.T) {
		for _, n := range []int{0, 5} {
			cfg := *valid
			cfg.NALULenSize = n
			_, err := BuildAVCDecoderConfigurationRecord(&cfg)
			assert.ErrorIs(t, err, ErrBadAVCConfig, "NALULenSize=%d", n)
		}
	})
}

// TestBuildAudioSpecificConfig_Golden: AAC-LC, 44100 Hz, stereo → {0x12, 0x10},
// the same AudioSpecificConfig TestParseAACConfig decodes (and that the
// browser-publish path base64-encodes as initData).
func TestBuildAudioSpecificConfig_Golden(t *testing.T) {
	cfg := &AACConfig{ObjectType: 2, SampleRate: 44100, ChannelConfig: 2}
	got, err := BuildAudioSpecificConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x12, 0x10}, got)
}

// TestBuildAudioSpecificConfig_RoundTrip asserts the builder is the exact
// inverse of parseAudioSpecificConfig across common AAC-LC configurations
// (representative sample rates, stereo and mono).
func TestBuildAudioSpecificConfig_RoundTrip(t *testing.T) {
	cfgs := []*AACConfig{
		{ObjectType: 2, SampleRate: 48000, ChannelConfig: 2},
		{ObjectType: 2, SampleRate: 44100, ChannelConfig: 2},
		{ObjectType: 2, SampleRate: 24000, ChannelConfig: 1},
		{ObjectType: 2, SampleRate: 16000, ChannelConfig: 2},
		{ObjectType: 2, SampleRate: 8000, ChannelConfig: 1},
	}
	for _, c := range cfgs {
		t.Run(fmt.Sprintf("%dHz_%dch", c.SampleRate, c.ChannelConfig), func(t *testing.T) {
			asc, err := BuildAudioSpecificConfig(c)
			require.NoError(t, err)
			parsed, err := parseAudioSpecificConfig(asc)
			require.NoError(t, err)
			assert.Equal(t, c.ObjectType, parsed.ObjectType)
			assert.Equal(t, c.SampleRate, parsed.SampleRate)
			assert.Equal(t, c.ChannelConfig, parsed.ChannelConfig)
		})
	}
}

func TestBuildAudioSpecificConfig_Errors(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := BuildAudioSpecificConfig(nil)
		assert.ErrorIs(t, err, ErrBadAACConfig)
	})
	t.Run("objectType out of range", func(t *testing.T) {
		for _, ot := range []byte{0, 32} {
			_, err := BuildAudioSpecificConfig(&AACConfig{ObjectType: ot, SampleRate: 44100, ChannelConfig: 2})
			assert.ErrorIs(t, err, ErrBadAACConfig, "objectType=%d", ot)
		}
	})
	t.Run("channelConfig out of range", func(t *testing.T) {
		for _, ch := range []int{0, 16} {
			_, err := BuildAudioSpecificConfig(&AACConfig{ObjectType: 2, SampleRate: 44100, ChannelConfig: ch})
			assert.ErrorIs(t, err, ErrBadAACConfig, "channelConfig=%d", ch)
		}
	})
	t.Run("unsupported sample rate", func(t *testing.T) {
		// 22050 is in the table; a non-indexed rate (e.g. 12345) must fail.
		_, err := BuildAudioSpecificConfig(&AACConfig{ObjectType: 2, SampleRate: 12345, ChannelConfig: 2})
		assert.ErrorIs(t, err, ErrBadAACConfig)
	})
}
