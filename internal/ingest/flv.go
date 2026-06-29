package ingest

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FLV/RTMP video tag constants.
const (
	flvCodecIDAVC   = 7 // H.264 / AVC
	flvAVCSeqHeader = 0 // AVCPacketType: sequence header
	flvAVCNALU      = 1 // AVCPacketType: NALU(s)
	flvKeyframe     = 1 // FrameType: keyframe
	flvInterframe   = 2 // FrameType: inter-frame

	flvSoundFormatAAC = 10 // SoundFormat: AAC
	flvAACSeqHeader   = 0  // AACPacketType: sequence header (AudioSpecificConfig)
	flvAACRaw         = 1  // AACPacketType: raw AAC frame data
)

// Errors returned by the FLV parsing functions.
var (
	ErrShortVideoTag = errors.New("flv: video tag too short")
	ErrShortAudioTag = errors.New("flv: audio tag too short")
	ErrNotAVC        = errors.New("flv: codec is not H.264/AVC")
	ErrNotAAC        = errors.New("flv: codec is not AAC")
	ErrBadAVCConfig  = errors.New("flv: invalid AVCDecoderConfigurationRecord")
	ErrBadAACConfig  = errors.New("flv: invalid AudioSpecificConfig")
)

// AVCConfig holds the parsed AVCDecoderConfigurationRecord extracted from
// an FLV video sequence header. It provides the SPS/PPS parameter sets
// needed for Annex-B conversion and the codec string for the MSF catalog.
type AVCConfig struct {
	// ProfileIDC, ProfileCompat, and LevelIDC form the codec string
	// "avc1.PPCCLL" (hex-encoded).
	ProfileIDC    byte
	ProfileCompat byte
	LevelIDC      byte
	// NALULenSize is the number of bytes used for NALU length fields in
	// AVCC data (typically 4).
	NALULenSize int
	// SPS holds the Sequence Parameter Set NAL units.
	SPS [][]byte
	// PPS holds the Picture Parameter Set NAL units.
	PPS [][]byte
	// Width and Height are derived from the SPS.
	Width  int
	Height int
}

// CodecString returns the codec identifier string (e.g. "avc1.64001f") used in
// MSF catalog entries. The "avc1" prefix denotes AVCC (length-prefixed) sample
// stream with parameter sets carried in the catalog initData, which is what
// RTMP ingest forwards unchanged and what a browser WebCodecs VideoDecoder
// consumes.
func (c *AVCConfig) CodecString() string {
	return fmt.Sprintf("avc1.%02x%02x%02x", c.ProfileIDC, c.ProfileCompat, c.LevelIDC)
}

// ParseAVCConfig parses an AVCDecoderConfigurationRecord from an FLV
// video sequence header tag payload (the raw FLV tag Data bytes starting
// with the standard header byte).
//
// FLV video tag layout (standard RTMP, CodecID=7):
//
//	byte 0: FrameType(4) | CodecID(4)
//	byte 1: AVCPacketType (0 = sequence header)
//	bytes 2-4: CompositionTimeOffset (ignored for seqhdr)
//	bytes 5+: AVCDecoderConfigurationRecord
func ParseAVCConfig(data []byte) (*AVCConfig, error) {
	if len(data) < 6 {
		return nil, ErrShortVideoTag
	}
	codecID := data[0] & 0x0F
	if codecID != flvCodecIDAVC {
		return nil, ErrNotAVC
	}
	if data[1] != flvAVCSeqHeader {
		return nil, fmt.Errorf("flv: expected sequence header (type 0), got %d", data[1])
	}
	return parseAVCDecoderConfigurationRecord(data[5:])
}

// BuildAVCDecoderConfigurationRecord serializes an [AVCConfig] into an
// ISO/IEC 14496-15 AVCDecoderConfigurationRecord — the codec initialization
// blob a browser WebCodecs VideoDecoder expects as its `description`, and the
// same shape the browser-publish path emits as Base64-encoded track initData.
// It is the inverse of [parseAVCDecoderConfigurationRecord]. Pure: no I/O, no
// globals, deterministic.
//
// Wire layout:
//
//	configurationVersion(1)                      = 0x01
//	AVCProfileIndication(1)                      = ProfileIDC
//	profile_compatibility(1)                     = ProfileCompat
//	AVCLevelIndication(1)                        = LevelIDC
//	reserved(0b111111) | lengthSizeMinusOne(2)   = 0xFC | (NALULenSize-1)
//	reserved(0b111)   | numOfSequenceParameterSets(5) = 0xE0 | len(SPS)
//	for each SPS: uint16BE(length) + NALU
//	numOfPictureParameterSets(1)                 = len(PPS)
//	for each PPS: uint16BE(length) + NALU
func BuildAVCDecoderConfigurationRecord(cfg *AVCConfig) ([]byte, error) {
	if cfg == nil {
		return nil, ErrBadAVCConfig
	}
	if cfg.NALULenSize < 1 || cfg.NALULenSize > 4 {
		return nil, ErrBadAVCConfig
	}
	if len(cfg.SPS) == 0 || len(cfg.SPS) > 31 {
		return nil, ErrBadAVCConfig
	}
	if len(cfg.PPS) > 255 {
		return nil, ErrBadAVCConfig
	}

	buf := make([]byte, 0, 6+1+sinkParamLen(cfg.SPS)+sinkParamLen(cfg.PPS))
	buf = append(buf,
		0x01,                  // configurationVersion
		cfg.ProfileIDC,        // AVCProfileIndication
		cfg.ProfileCompat,     // profile_compatibility
		cfg.LevelIDC,          // AVCLevelIndication
		0xFC|byte(cfg.NALULenSize-1), // reserved | lengthSizeMinusOne
		0xE0|byte(len(cfg.SPS)),      // reserved | numOfSequenceParameterSets
	)
	var lenBuf [2]byte
	for _, sps := range cfg.SPS {
		if len(sps) > 0xFFFF {
			return nil, ErrBadAVCConfig
		}
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(sps)))
		buf = append(buf, lenBuf[:]...)
		buf = append(buf, sps...)
	}
	buf = append(buf, byte(len(cfg.PPS))) // numOfPictureParameterSets
	for _, pps := range cfg.PPS {
		if len(pps) > 0xFFFF {
			return nil, ErrBadAVCConfig
		}
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(pps)))
		buf = append(buf, lenBuf[:]...)
		buf = append(buf, pps...)
	}
	return buf, nil
}

// sinkParamLen returns the total wire bytes for a NALU slice: 2-byte length
// prefix per entry. It is a small helper for sizing the output buffer.
func sinkParamLen(nalus [][]byte) int {
	n := 2 * len(nalus)
	for _, nalu := range nalus {
		n += len(nalu)
	}
	return n
}

// parseAVCDecoderConfigurationRecord parses the ISO 14496-15 record.
func parseAVCDecoderConfigurationRecord(buf []byte) (*AVCConfig, error) {
	// Minimum: version(1) + profile(1) + compat(1) + level(1) + flags(1) + numSPS(1) = 6
	if len(buf) < 6 {
		return nil, ErrBadAVCConfig
	}
	cfg := &AVCConfig{
		ProfileIDC:    buf[1],
		ProfileCompat: buf[2],
		LevelIDC:      buf[3],
		NALULenSize:   int(buf[4]&0x03) + 1,
	}

	numSPS := int(buf[5] & 0x1F)

	// Pass 1: calculate total byte length required for SPS/PPS parameters.
	var totalLen int
	calcOff := 6
	for i := range numSPS {
		_ = i
		if calcOff+2 > len(buf) {
			return nil, ErrBadAVCConfig
		}
		spsLen := int(binary.BigEndian.Uint16(buf[calcOff:]))
		calcOff += 2
		if calcOff+spsLen > len(buf) {
			return nil, ErrBadAVCConfig
		}
		totalLen += spsLen
		calcOff += spsLen
	}

	if calcOff >= len(buf) {
		return nil, ErrBadAVCConfig
	}
	numPPS := int(buf[calcOff])
	calcOff++
	for i := range numPPS {
		_ = i
		if calcOff+2 > len(buf) {
			return nil, ErrBadAVCConfig
		}
		ppsLen := int(binary.BigEndian.Uint16(buf[calcOff:]))
		calcOff += 2
		if calcOff+ppsLen > len(buf) {
			return nil, ErrBadAVCConfig
		}
		totalLen += ppsLen
		calcOff += ppsLen
	}

	// Allocate a single contiguous byte array and required slice capacity.
	paramBytes := make([]byte, totalLen)
	cfg.SPS = make([][]byte, 0, numSPS)
	cfg.PPS = make([][]byte, 0, numPPS)

	// Pass 2: copy data and construct slices.
	off := 6
	var byteOff int
	for i := range numSPS {
		_ = i
		spsLen := int(binary.BigEndian.Uint16(buf[off:]))
		off += 2
		copy(paramBytes[byteOff:], buf[off:off+spsLen])
		cfg.SPS = append(cfg.SPS, paramBytes[byteOff:byteOff+spsLen:byteOff+spsLen])
		byteOff += spsLen
		off += spsLen
	}

	numPPS = int(buf[off])
	off++
	for i := range numPPS {
		_ = i
		ppsLen := int(binary.BigEndian.Uint16(buf[off:]))
		off += 2
		copy(paramBytes[byteOff:], buf[off:off+ppsLen])
		cfg.PPS = append(cfg.PPS, paramBytes[byteOff:byteOff+ppsLen:byteOff+ppsLen])
		byteOff += ppsLen
		off += ppsLen
	}

	// Derive width and height from the first SPS if available.
	if len(cfg.SPS) > 0 {
		cfg.Width, cfg.Height = parseSPSDimensions(cfg.SPS[0])
	}

	return cfg, nil
}

// parseFLVVideoCTS extracts the 24-bit signed Composition Time Offset (in ms)
// from an FLV AVC NALU tag's bytes 2-4. RTMP ingest forwards AVCC NALUs
// unchanged and uses this to compute presentation timestamps (PTS = DTS + CTS),
// preserving B-frame timing.
//
// FLV video NALU tag layout:
//
//	byte 0: FrameType(4) | CodecID(4)
//	byte 1: AVCPacketType (1 = NALU)
//	bytes 2-4: CompositionTimeOffset (24-bit signed, in ms)
//	bytes 5+: one or more length-prefixed NALUs (forwarded unchanged as AVCC)
func parseFLVVideoCTS(data []byte) int32 {
	cts := int32(data[2])<<16 | int32(data[3])<<8 | int32(data[4])
	if cts&0x800000 != 0 {
		cts |= ^0xFFFFFF // sign-extend
	}
	return cts
}

// AACConfig holds the parsed AudioSpecificConfig extracted from an FLV
// audio sequence header.
type AACConfig struct {
	// ObjectType is the AAC audio object type (e.g. 2 = AAC-LC).
	ObjectType byte
	// SampleRate is the audio sample rate in Hz.
	SampleRate int
	// ChannelConfig is the channel configuration (e.g. 2 = stereo).
	ChannelConfig int
}

// aacSampleRates maps the 4-bit sampling frequency index to Hz.
var aacSampleRates = [...]int{
	96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050,
	16000, 12000, 11025, 8000, 7350,
}

// CodecString returns the codec identifier string for the MSF catalog
// (e.g. "mp4a.40.2" for AAC-LC).
func (c *AACConfig) CodecString() string {
	return fmt.Sprintf("mp4a.40.%d", c.ObjectType)
}

// ParseAACConfig parses the AudioSpecificConfig from an FLV audio sequence
// header tag. The data parameter is the raw FLV audio tag Data bytes.
//
// FLV audio tag layout:
//
//	byte 0: SoundFormat(4) | SoundRate(2) | SoundSize(1) | SoundType(1)
//	byte 1: AACPacketType (0 = AudioSpecificConfig)
//	bytes 2+: AudioSpecificConfig
func ParseAACConfig(data []byte) (*AACConfig, error) {
	if len(data) < 4 {
		return nil, ErrShortAudioTag
	}
	soundFormat := (data[0] >> 4) & 0x0F
	if soundFormat != flvSoundFormatAAC {
		return nil, ErrNotAAC
	}
	if data[1] != flvAACSeqHeader {
		return nil, fmt.Errorf("flv: expected AAC sequence header (type 0), got %d", data[1])
	}

	asc := data[2:]
	return parseAudioSpecificConfig(asc)
}

// parseAudioSpecificConfig parses a raw AAC AudioSpecificConfig (ISO 14496-3)
// into an [AACConfig]. It is shared by the FLV and RTSP ingest paths, which
// carry the same AudioSpecificConfig in different containers.
//
// AudioSpecificConfig layout (first 13 bits):
//
//	bits 0-4:   audioObjectType
//	bits 5-8:   samplingFrequencyIndex
//	bits 9-12:  channelConfiguration
func parseAudioSpecificConfig(asc []byte) (*AACConfig, error) {
	if len(asc) < 2 {
		return nil, errors.New("flv: AudioSpecificConfig too short")
	}

	objectType := (asc[0] >> 3) & 0x1F
	freqIdx := ((asc[0] & 0x07) << 1) | (asc[1] >> 7)
	chanCfg := (asc[1] >> 3) & 0x0F

	sampleRate := 0
	if int(freqIdx) < len(aacSampleRates) {
		sampleRate = aacSampleRates[freqIdx]
	}

	return &AACConfig{
		ObjectType:    objectType,
		SampleRate:    sampleRate,
		ChannelConfig: int(chanCfg),
	}, nil
}

// aacSampleRateIndex is the reverse of [aacSampleRates]: it maps a sample rate
// in Hz to its 4-bit samplingFrequencyIndex, used when serializing an
// AudioSpecificConfig. ok is false for rates not in the indexed table (which
// would require the explicit 24-bit frequency form, unsupported here — all
// common AAC rates are indexed).
func aacSampleRateIndex(hz int) (idx int, ok bool) {
	for i, r := range aacSampleRates {
		if r == hz {
			return i, true
		}
	}
	return 0, false
}

// BuildAudioSpecificConfig serializes an [AACConfig] into an MPEG-4
// AudioSpecificConfig (ISO/IEC 14496-3) — the codec initialization blob a
// browser WebCodecs AudioDecoder expects as its `description`, and the same
// shape the browser-publish path emits as Base64-encoded track initData. It is
// the inverse of [parseAudioSpecificConfig] for the common (indexed-rate,
// AAC-LC and friends) configurations. Pure: no I/O, no globals, deterministic.
//
// Two-byte layout (first 13 bits):
//
//	bits 0-4:  audioObjectType
//	bits 5-8:  samplingFrequencyIndex (mapped from SampleRate)
//	bits 9-12: channelConfiguration
func BuildAudioSpecificConfig(cfg *AACConfig) ([]byte, error) {
	if cfg == nil {
		return nil, ErrBadAACConfig
	}
	if cfg.ObjectType < 1 || cfg.ObjectType > 31 {
		return nil, ErrBadAACConfig
	}
	if cfg.ChannelConfig < 1 || cfg.ChannelConfig > 15 {
		return nil, ErrBadAACConfig
	}
	freqIdx, ok := aacSampleRateIndex(cfg.SampleRate)
	if !ok {
		return nil, ErrBadAACConfig
	}
	return []byte{
		(cfg.ObjectType << 3) | byte(freqIdx>>1),
		(byte(freqIdx&0x01) << 7) | byte(cfg.ChannelConfig<<3),
	}, nil
}

// StripFLVAudioHeader removes the 2-byte FLV audio tag header (format
// byte + AACPacketType) from a raw AAC data packet, returning just the
// raw AAC frame bytes. It returns an error if the packet is not AAC raw
// data.
func StripFLVAudioHeader(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, ErrShortAudioTag
	}
	soundFormat := (data[0] >> 4) & 0x0F
	if soundFormat != flvSoundFormatAAC {
		return nil, ErrNotAAC
	}
	if data[1] != flvAACRaw {
		return nil, fmt.Errorf("flv: expected AAC raw data (type 1), got %d", data[1])
	}
	return data[2:], nil
}

// IsVideoSequenceHeader reports whether the FLV video tag data is an AVC
// sequence header (AVCDecoderConfigurationRecord).
func IsVideoSequenceHeader(data []byte) bool {
	return len(data) >= 2 && data[0]&0x0F == flvCodecIDAVC && data[1] == flvAVCSeqHeader
}

// IsAudioSequenceHeader reports whether the FLV audio tag data is an AAC
// sequence header (AudioSpecificConfig).
func IsAudioSequenceHeader(data []byte) bool {
	return len(data) >= 2 && (data[0]>>4)&0x0F == flvSoundFormatAAC && data[1] == flvAACSeqHeader
}

// parseSPSDimensions extracts the width and height from an H.264 SPS NAL
// unit. It handles the basic profile_idc cases; for unusual cropping or
// frame_mbs_only_flag=0, the returned dimensions may be approximate.
func parseSPSDimensions(sps []byte) (width, height int) {
	if len(sps) < 4 {
		return 0, 0
	}
	r := &bitReader{data: sps, off: 0}

	// NAL header: forbidden_zero_bit(1) + nal_ref_idc(2) + nal_unit_type(5)
	r.skip(8)

	profileIDC := r.readBits(8)
	r.skip(8) // constraint_set_flags + reserved
	r.skip(8) // level_idc

	r.readExpGolomb() // seq_parameter_set_id

	// For High, High 10, High 4:2:2, High 4:4:4, etc.
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 ||
		profileIDC == 244 || profileIDC == 44 || profileIDC == 83 ||
		profileIDC == 86 || profileIDC == 118 || profileIDC == 128 {
		chromaFormatIDC := r.readExpGolomb()
		if chromaFormatIDC == 3 {
			r.skip(1) // separate_colour_plane_flag
		}
		r.readExpGolomb() // bit_depth_luma_minus8
		r.readExpGolomb() // bit_depth_chroma_minus8
		r.skip(1)         // qpprime_y_zero_transform_bypass_flag
		scalingMatrixPresent := r.readBits(1)
		if scalingMatrixPresent == 1 {
			count := 8
			if chromaFormatIDC == 3 {
				count = 12
			}
			for i := 0; i < count; i++ {
				if r.readBits(1) == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					skipScalingList(r, size)
				}
			}
		}
	}

	r.readExpGolomb() // log2_max_frame_num_minus4
	picOrderCntType := r.readExpGolomb()
	switch picOrderCntType {
	case 0:
		r.readExpGolomb() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		r.skip(1)        // delta_pic_order_always_zero_flag
		r.readSignedEG() // offset_for_non_ref_pic
		r.readSignedEG() // offset_for_top_to_bottom_field
		n := r.readExpGolomb()
		for i := 0; i < int(n); i++ {
			r.readSignedEG()
		}
	}

	r.readExpGolomb() // max_num_ref_frames
	r.skip(1)         // gaps_in_frame_num_value_allowed_flag

	picWidthMbs := r.readExpGolomb() + 1
	picHeightMapUnits := r.readExpGolomb() + 1
	frameMbsOnlyFlag := r.readBits(1)
	if frameMbsOnlyFlag == 0 {
		r.skip(1) // mb_adaptive_frame_field_flag
	}
	r.skip(1) // direct_8x8_inference_flag

	frameCropFlag := r.readBits(1)
	cropLeft, cropRight, cropTop, cropBottom := 0, 0, 0, 0
	if frameCropFlag == 1 {
		cropLeft = int(r.readExpGolomb())
		cropRight = int(r.readExpGolomb())
		cropTop = int(r.readExpGolomb())
		cropBottom = int(r.readExpGolomb())
	}

	width = int(picWidthMbs)*16 - (cropLeft+cropRight)*2
	height = (2 - int(frameMbsOnlyFlag)) * int(picHeightMapUnits) * 16
	height -= (cropTop + cropBottom) * 2

	return width, height
}

// bitReader reads bits from a byte slice. It is designed for SPS parsing
// and does not handle RBSP emulation prevention (0x03 bytes).
type bitReader struct {
	data []byte
	off  int // bit offset
}

func (r *bitReader) readBits(n int) uint32 {
	var val uint32
	for range n {
		bytePos := r.off / 8
		bitPos := 7 - (r.off % 8)
		if bytePos < len(r.data) {
			val = (val << 1) | uint32((r.data[bytePos]>>bitPos)&1)
		} else {
			val <<= 1
		}
		r.off++
	}
	return val
}

func (r *bitReader) skip(n int) {
	r.off += n
}

func (r *bitReader) readExpGolomb() uint32 {
	zeros := 0
	for r.readBits(1) == 0 {
		zeros++
		if zeros > 31 {
			return 0
		}
	}
	if zeros == 0 {
		return 0
	}
	return (1 << zeros) - 1 + r.readBits(zeros)
}

func (r *bitReader) readSignedEG() int32 {
	v := r.readExpGolomb()
	if v%2 == 0 {
		return -int32(v / 2)
	}
	return int32((v + 1) / 2)
}

func skipScalingList(r *bitReader, size int) {
	lastScale := 8
	nextScale := 8
	for range size {
		if nextScale != 0 {
			delta := r.readSignedEG()
			nextScale = (lastScale + int(delta) + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
}
