package ingest

import (
	"encoding/binary"
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
	assert.Equal(t, "avc3.64001f", cfg.CodecString())
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

func TestAVCCToAnnexB_Keyframe(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F}
	pps := []byte{0x68, 0xEB}
	cfg := &AVCConfig{
		NALULenSize: 4,
		SPS:         [][]byte{sps},
		PPS:         [][]byte{pps},
	}

	idrNALU := []byte{0x65, 0xAA, 0xBB, 0xCC}
	data := buildAVCNALUTag(1, 0, idrNALU)

	annexB, cts, err := AVCCToAnnexB(data, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(0), cts)

	// Expected: startCode + SPS + startCode + PPS + startCode + IDR
	expected := make([]byte, 0)
	expected = append(expected, startCode...)
	expected = append(expected, sps...)
	expected = append(expected, startCode...)
	expected = append(expected, pps...)
	expected = append(expected, startCode...)
	expected = append(expected, idrNALU...)

	assert.Equal(t, expected, annexB)
}

func TestAVCCToAnnexB_InterFrame(t *testing.T) {
	cfg := &AVCConfig{
		NALULenSize: 4,
		SPS:         [][]byte{{0x67}},
		PPS:         [][]byte{{0x68}},
	}

	nalu := []byte{0x41, 0x01, 0x02}
	data := buildAVCNALUTag(2, 33, nalu) // inter-frame, CTS=33

	annexB, cts, err := AVCCToAnnexB(data, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(33), cts)

	// Inter-frames should NOT have SPS/PPS prepended.
	expected := append(startCode, nalu...)
	assert.Equal(t, expected, annexB)
}

func TestAVCCToAnnexB_MultipleNALUs(t *testing.T) {
	cfg := &AVCConfig{NALULenSize: 4}

	nalu1 := []byte{0x41, 0x01}
	nalu2 := []byte{0x01, 0x02, 0x03}

	// Build tag with two NALUs
	tag := []byte{
		0x27,             // FrameType=2 (inter), CodecID=7
		0x01,             // AVCPacketType = NALU
		0x00, 0x00, 0x00, // CTS = 0
	}
	len1 := make([]byte, 4)
	binary.BigEndian.PutUint32(len1, uint32(len(nalu1)))
	tag = append(tag, len1...)
	tag = append(tag, nalu1...)
	len2 := make([]byte, 4)
	binary.BigEndian.PutUint32(len2, uint32(len(nalu2)))
	tag = append(tag, len2...)
	tag = append(tag, nalu2...)

	annexB, _, err := AVCCToAnnexB(tag, cfg)
	require.NoError(t, err)

	expected := make([]byte, 0)
	expected = append(expected, startCode...)
	expected = append(expected, nalu1...)
	expected = append(expected, startCode...)
	expected = append(expected, nalu2...)

	assert.Equal(t, expected, annexB)
}

func TestAVCCToAnnexB_NegativeCTS(t *testing.T) {
	cfg := &AVCConfig{NALULenSize: 4}
	nalu := []byte{0x41}
	// CTS = -1 → SI24 = 0xFFFFFF
	data := buildAVCNALUTag(2, -1, nalu)

	_, cts, err := AVCCToAnnexB(data, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(-1), cts)
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
