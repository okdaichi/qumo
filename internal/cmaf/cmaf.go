// Package cmaf packages encoded media frames into fragmented MP4.
//
// It exists because the wire and the archive want different things. MoQ carries
// LOC — an encoded frame with a timestamp and almost nothing else — which is
// what makes sub-second playback possible and what a WebCodecs decoder consumes
// directly. HLS wants CMAF: an initialization segment describing the codec, and
// media fragments carrying samples with durations. Neither format is wrong; they
// serve different consumers.
//
// Packaging therefore happens here, at the subscriber, rather than at the
// publisher. A publisher that muxed CMAF would push its GOP batching onto every
// consumer including the real-time one, and would store a representation that no
// longer matches what it sent. Converting here keeps the live path unbatched and
// leaves one publisher feeding both.
package cmaf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Timescale is the unit fragment timestamps are expressed in.
//
// LOC timestamps come from WebCodecs, which counts microseconds, so a
// microsecond timescale carries them with no scaling and no rounding. A coarser
// one — 90 kHz, say — would round every sample and accumulate the error along
// the timeline.
const Timescale = 1_000_000

// trackID is the single video track every init segment describes. Audio is not
// packaged yet; when it is, it becomes track 2 rather than a second file.
const trackID = 1

// Frame is one encoded frame as it arrived over the wire: a presentation
// timestamp in [Timescale] units, and the encoded bytes.
//
// Sync is whether the frame opens a group. LOC carries no such flag — a MoQ
// group begins at each keyframe, so the group boundary is the signal, and the
// caller passes what it observed rather than this package guessing.
type Frame struct {
	Timestamp uint64
	Sync      bool
	Data      []byte
}

// Packager turns groups of frames into CMAF. One Packager serves one track for
// as long as its codec configuration holds; a publisher that restarts with a
// different configuration needs a new one, which is also a new ledger epoch.
type Packager struct {
	init     []byte
	sequence uint32

	// decodeTime is where the next fragment sits on the track's timeline,
	// accumulated from the extents already packaged.
	//
	// It deliberately does not follow the encoder's own clock. A capture
	// timestamp starts wherever the browser's clock happened to be — some
	// thousands of seconds in — while the manifest describes a track that
	// starts at zero and advances by each segment's duration. A player seeks to
	// where the manifest says the media is, so a fragment stamped with the
	// capture clock lands nowhere near it: the buffer fills, nothing overlaps
	// the seek position, and playback stalls with no error to report.
	decodeTime uint64
}

// VideoConfig is what a catalog states about a track, and all this package needs
// to describe it: the WebCodecs codec string, and the coded picture size.
type VideoConfig struct {
	// Codec is the WebCodecs identifier, e.g. "vp09.00.10.08" or "avc1.42E01E".
	Codec string

	Width  uint16
	Height uint16

	// Description is the codec's out-of-band configuration, when it has one:
	// the AVC/HEVC parameter sets a decoder needs before the first frame. VP9
	// and AV1 carry theirs in the codec string, so this is empty for them.
	Description []byte
}

// NewPackager builds the initialization segment for cfg, which fixes the codec
// for every fragment the packager goes on to produce.
func NewPackager(cfg VideoConfig) (*Packager, error) {
	if cfg.Width == 0 || cfg.Height == 0 {
		return nil, fmt.Errorf("cmaf: track has no picture size (%dx%d)", cfg.Width, cfg.Height)
	}

	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(Timescale, "video", "und")
	trak := init.Moov.Trak

	if err := describe(trak, cfg); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		return nil, fmt.Errorf("cmaf: encode init segment: %w", err)
	}
	return &Packager{init: buf.Bytes()}, nil
}

// Init is the initialization segment: ftyp + moov, the bytes an HLS
// EXT-X-MAP points at.
func (p *Packager) Init() []byte {
	return p.init
}

// Fragment packages one group of frames into a moof+mdat media fragment, and
// reports the media extent it covers.
//
// Sample durations come from the gaps between timestamps, which leaves the last
// frame of a group without one — its successor belongs to the next group and has
// not arrived. Rather than hold the fragment back a whole GOP to learn it, the
// last sample takes the mean of the others: the error is under one frame
// interval, and it cannot accumulate because the next fragment re-anchors the
// timeline from its own first timestamp.
func (p *Packager) Fragment(frames []Frame) (data []byte, duration uint64, err error) {
	if len(frames) == 0 {
		return nil, 0, fmt.Errorf("cmaf: no frames to package")
	}

	durations := sampleDurations(frames)
	var total uint64
	for _, d := range durations {
		total += uint64(d)
	}

	p.sequence++
	frag, err := mp4.CreateFragment(p.sequence, trackID)
	if err != nil {
		return nil, 0, fmt.Errorf("cmaf: create fragment: %w", err)
	}

	// Fragments run back to back from zero, which is the timeline the manifest
	// publishes: the ledger's media time is the same running sum of the same
	// durations, so a fragment says exactly what the segment listing says about
	// where it belongs.
	decodeTime := p.decodeTime
	for i, f := range frames {
		flags := mp4.NonSyncSampleFlags
		if f.Sync {
			flags = mp4.SyncSampleFlags
		}
		frag.AddFullSample(mp4.FullSample{
			Sample:     mp4.NewSample(flags, durations[i], uint32(len(f.Data)), 0),
			DecodeTime: decodeTime,
			Data:       f.Data,
		})
		decodeTime += uint64(durations[i])
	}

	var buf bytes.Buffer
	if err := frag.Encode(&buf); err != nil {
		return nil, 0, fmt.Errorf("cmaf: encode fragment: %w", err)
	}
	p.decodeTime += total
	return buf.Bytes(), total, nil
}

// sampleDurations derives each frame's extent from the gap to the next one. A
// single frame, or one whose successor does not advance the clock, falls back to
// the mean — with nothing to average, to one frame at 30fps, which is a guess
// but a bounded one about a fragment that holds a single sample.
func sampleDurations(frames []Frame) []uint32 {
	durations := make([]uint32, len(frames))
	if len(frames) == 1 {
		durations[0] = Timescale / 30
		return durations
	}

	var sum uint64
	for i := range len(frames) - 1 {
		delta := frames[i+1].Timestamp - frames[i].Timestamp
		durations[i] = uint32(delta)
		sum += delta
	}
	durations[len(frames)-1] = uint32(sum / uint64(len(frames)-1))
	return durations
}

// describe attaches the codec-specific sample description to the track.
func describe(trak *mp4.TrakBox, cfg VideoConfig) error {
	switch family(cfg.Codec) {
	case "vp09", "vp08":
		vpcc, err := vpxConfig(cfg.Codec)
		if err != nil {
			return err
		}
		if err := trak.SetVPxDescriptor(family(cfg.Codec), vpcc, cfg.Width, cfg.Height); err != nil {
			return fmt.Errorf("cmaf: describe %s: %w", cfg.Codec, err)
		}
		return nil

	case "avc1", "avc3":
		sps, pps, err := avcParameterSets(cfg.Description)
		if err != nil {
			return err
		}
		if err := trak.SetAVCDescriptor("avc1", sps, pps, true); err != nil {
			return fmt.Errorf("cmaf: describe %s: %w", cfg.Codec, err)
		}
		return nil

	default:
		return fmt.Errorf("cmaf: unsupported codec %q", cfg.Codec)
	}
}

// family is the four-character prefix of a WebCodecs codec string.
func family(codec string) string {
	if len(codec) < 4 {
		return codec
	}
	return codec[:4]
}

// vpxConfig builds a vpcC from a WebCodecs VP8/VP9 codec string, which states
// profile, level and bit depth as dot-separated decimal fields:
//
//	vp09.<profile>.<level>.<bitDepth>[.<chroma>.<primaries>.<transfer>.<matrix>.<fullRange>]
//
// Only the first three are required. The colour fields default to BT.709, which
// is what a browser camera capture produces and what the encoder assumed when
// the publisher omitted them.
func vpxConfig(codec string) (*mp4.VppCBox, error) {
	parts := strings.Split(codec, ".")
	if len(parts) < 4 {
		return nil, fmt.Errorf("cmaf: codec %q states no profile, level and bit depth", codec)
	}

	field := func(i int, name string) (byte, error) {
		v, err := strconv.ParseUint(parts[i], 10, 8)
		if err != nil {
			return 0, fmt.Errorf("cmaf: codec %q has an unreadable %s: %w", codec, name, err)
		}
		return byte(v), nil
	}

	profile, err := field(1, "profile")
	if err != nil {
		return nil, err
	}
	level, err := field(2, "level")
	if err != nil {
		return nil, err
	}
	bitDepth, err := field(3, "bit depth")
	if err != nil {
		return nil, err
	}

	vpcc := &mp4.VppCBox{
		Version:  1,
		Profile:  profile,
		Level:    level,
		BitDepth: bitDepth,
		// 4:2:0 co-sited, BT.709 primaries/transfer/matrix, limited range.
		ChromaSubsampling:       1,
		ColourPrimaries:         1,
		TransferCharacteristics: 1,
		MatrixCoefficients:      1,
		VideoFullRangeFlag:      0,
	}
	for i, target := range []*byte{
		&vpcc.ChromaSubsampling, &vpcc.ColourPrimaries,
		&vpcc.TransferCharacteristics, &vpcc.MatrixCoefficients, &vpcc.VideoFullRangeFlag,
	} {
		if idx := 4 + i; idx < len(parts) {
			v, err := field(idx, "colour field")
			if err != nil {
				return nil, err
			}
			*target = v
		}
	}
	return vpcc, nil
}

// avcParameterSets splits an AVCDecoderConfigurationRecord — what WebCodecs
// hands over as decoderConfig.description — into the SPS and PPS an avcC needs.
func avcParameterSets(description []byte) (sps, pps [][]byte, err error) {
	if len(description) == 0 {
		return nil, nil, fmt.Errorf(
			"cmaf: an AVC track needs its parameter sets, and the catalog carries none")
	}
	record, err := avc.DecodeAVCDecConfRec(description)
	if err != nil {
		return nil, nil, fmt.Errorf("cmaf: read AVC configuration: %w", err)
	}
	return record.SPSnalus, record.PPSnalus, nil
}
