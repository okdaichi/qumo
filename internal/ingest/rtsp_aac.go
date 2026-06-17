package ingest

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// aacFrameSamples is the number of audio samples in one AAC-LC frame. Every
// independently-decodable AAC frame the RTSP path pushes advances the
// presentation timestamp by this many samples at the audio clock rate.
const aacFrameSamples = 1024

var (
	errShortRTPPayload  = errors.New("rtsp: mpeg4-generic RTP payload too short")
	errBadAUHeaderField = errors.New("rtsp: invalid mpeg4-generic AU-header field widths")
)

// aacAccessUnit is a single decoded AAC access unit together with its
// presentation timestamp in microseconds.
type aacAccessUnit struct {
	data []byte
	pts  int64
}

// aacDepacketizer reassembles AAC access units from mpeg4-generic
// (RFC 3640) RTP payloads as produced by RTSP publishers such as ffmpeg.
//
// It supports the common AAC-hbr case where each RTP packet carries one or
// more complete, unfragmented access units. AU fragmentation (an access unit
// split across several RTP packets) is not handled; ffmpeg does not emit it
// for AAC over interleaved TCP.
type aacDepacketizer struct {
	// clockRate is the audio sample rate in Hz. RTP timestamps, expressed in
	// clock ticks, are mapped to microseconds with it.
	clockRate int

	// AU-header field widths in bits (RFC 3640 §3.2.1). Defaults match the
	// values ffmpeg advertises for AAC-hbr.
	sizeLength  int
	indexLength int
	indexDelta  int
}

// parseAACConfigFromFmtp extracts the AAC AudioSpecificConfig carried in the
// "config" parameter of an mpeg4-generic fmtp string and parses it into an
// [AACConfig]. If no usable config is present it falls back to a stereo
// 44.1 kHz AAC-LC default so the audio track is still announced.
func parseAACConfigFromFmtp(fmtp string) *AACConfig {
	if idx := strings.Index(fmtp, "config="); idx != -1 {
		configHex := strings.Split(fmtp[idx+7:], ";")[0]
		if asc, err := hex.DecodeString(configHex); err == nil {
			if cfg, err := parseAudioSpecificConfig(asc); err == nil {
				return cfg
			}
		}
	}
	return &AACConfig{ObjectType: 2, SampleRate: 44100, ChannelConfig: 2}
}

// newAACDepacketizer builds a depacketizer from an SDP fmtp string and the
// audio clock rate (the sample rate parsed from the AudioSpecificConfig).
// Missing field widths fall back to the ffmpeg AAC-hbr defaults.
func newAACDepacketizer(fmtp string, clockRate int) *aacDepacketizer {
	d := &aacDepacketizer{
		clockRate:   clockRate,
		sizeLength:  13,
		indexLength: 3,
		indexDelta:  3,
	}
	params := parseMpeg4GenericFmtp(fmtp)
	if n, ok := atoiParam(params, "sizelength"); ok {
		d.sizeLength = n
	}
	if n, ok := atoiParam(params, "indexlength"); ok {
		d.indexLength = n
	}
	if n, ok := atoiParam(params, "indexdeltalength"); ok {
		d.indexDelta = n
	} else {
		// indexDeltaLength is almost always equal to indexLength; treat them
		// as equal when only one is advertised.
		d.indexDelta = d.indexLength
	}
	return d
}

// depacketize extracts the AAC access units carried by a single mpeg4-generic
// RTP payload. The supplied timestamp applies to the first access unit; each
// subsequent access unit is one AAC frame (aacFrameSamples samples) later.
func (d *aacDepacketizer) depacketize(payload []byte, ts uint32) ([]aacAccessUnit, error) {
	if len(payload) < 2 {
		return nil, errShortRTPPayload
	}

	// AU-headers-length is the size of the following AU-headers in bits,
	// excluding the 16-bit length field itself.
	auHeadersLenBits := int(binary.BigEndian.Uint16(payload[0:2]))

	// Solve for the access-unit count from the AU-headers bit budget:
	//   bits = numAUs*sizeLength + indexLength + (numAUs-1)*indexDelta
	headerWidth := d.sizeLength + d.indexDelta
	if d.sizeLength <= 0 || headerWidth <= 0 {
		return nil, errBadAUHeaderField
	}
	numAUs := (auHeadersLenBits - d.indexLength + d.indexDelta) / headerWidth
	if numAUs <= 0 {
		return nil, errBadAUHeaderField
	}

	// Bit offset of each access unit's sizeLength-bit size field. The first
	// header uses indexLength index bits; later headers use indexDelta.
	sizeBitOff := make([]int, numAUs)
	{
		off := 16 // bits, after the AU-headers-length field
		for i := 0; i < numAUs; i++ {
			sizeBitOff[i] = off
			off += d.sizeLength
			if i == 0 {
				off += d.indexLength
			} else {
				off += d.indexDelta
			}
		}
	}

	// Access-unit data follows the 16-bit length field and the AU-headers,
	// padded to a byte boundary.
	dataOff := 2 + (auHeadersLenBits+7)/8
	if dataOff > len(payload) {
		return nil, errShortRTPPayload
	}

	aus := make([]aacAccessUnit, 0, numAUs)
	basePTS := d.toMicros(ts)
	frameDelta := aacFrameDurationMicros(d.clockRate)
	for i := 0; i < numAUs; i++ {
		size := int(readBits(payload, sizeBitOff[i], d.sizeLength))
		if dataOff+size > len(payload) {
			return nil, errShortRTPPayload
		}
		aus = append(aus, aacAccessUnit{
			data: payload[dataOff : dataOff+size : dataOff+size],
			pts:  basePTS + int64(i)*frameDelta,
		})
		dataOff += size
	}
	return aus, nil
}

// toMicros maps an RTP timestamp (in audio clock ticks) to microseconds.
func (d *aacDepacketizer) toMicros(ts uint32) int64 {
	if d.clockRate <= 0 {
		return 0
	}
	return int64(ts) * 1_000_000 / int64(d.clockRate)
}

// aacFrameDurationMicros is the duration of one AAC-LC frame at clockRate,
// in microseconds.
func aacFrameDurationMicros(clockRate int) int64 {
	if clockRate <= 0 {
		return 0
	}
	return int64(aacFrameSamples) * 1_000_000 / int64(clockRate)
}

// readBits reads n bits from data starting at the given MSB-first bit offset.
func readBits(data []byte, bitOff, n int) uint {
	var v uint
	for i := 0; i < n; i++ {
		bitIdx := bitOff + i
		byteIdx := bitIdx >> 3
		if byteIdx >= len(data) {
			break
		}
		bitInByte := 7 - (bitIdx & 7)
		v = (v << 1) | uint((data[byteIdx]>>bitInByte)&1)
	}
	return v
}

// parseMpeg4GenericFmtp splits an SDP fmtp value into a lower-cased key map.
func parseMpeg4GenericFmtp(fmtp string) map[string]string {
	params := make(map[string]string)
	for _, p := range strings.Split(fmtp, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			continue
		}
		params[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return params
}

func atoiParam(params map[string]string, key string) (int, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
