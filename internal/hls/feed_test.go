package hls

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/msf"

	"github.com/okdaichi/qumo-ledger/ledger"
	"github.com/qumo-dev/qumo/internal/cmaf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groupInfo places a group at the media time the caller has accumulated, so a
// dropped MoQ group (a gappy sequence) leaves no hole in the timeline. The
// sequence is the group's identity; the epoch is stamped by the writer.
func Test_groupInfo(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	const du int64 = 180000 // two seconds at 90 kHz

	tests := map[string]struct {
		seq       int64
		mediaTime int64
	}{
		"first group":  {seq: 5, mediaTime: 0},
		"second group": {seq: 6, mediaTime: 180000},
		"gappy seq":    {seq: 99, mediaTime: 360000},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := groupInfo(uint64(tt.seq), tt.mediaTime, du, 30, now)

			assert.Equal(t, ledger.NewGroupID(0, uint64(tt.seq)), got.ID,
				"the producer sequence is the identity; the epoch is stamped by the writer")
			assert.Equal(t, tt.mediaTime, got.MediaTime,
				"media time is the caller's running total, not the gappy producer sequence")
			assert.Equal(t, du, got.Duration)
			assert.Equal(t, now.UnixNano(), got.Wallclock)
			assert.Equal(t, uint64(30), got.ObjectCount)
		})
	}
}

// trackSchema describes what the egress stores — the fragments it packages —
// rather than what arrived over the wire.
func Test_trackSchema(t *testing.T) {
	s := trackSchema(&msf.Track{
		Name: "video", Packaging: msf.PackagingLOC,
		MimeType: "video/mp4", Codec: "vp09.00.10.08",
	})

	assert.Equal(t, uint32(cmaf.Timescale), s.Timescale,
		"the packager's timescale, since the packager writes the payloads")
	assert.Equal(t, "fmp4", s.Encoding)
	assert.Equal(t, "video/mp4", s.MIME)
	assert.Equal(t, ledger.TimeSourceIngest, s.TimeSource)
}

// packagerForTrack needs a picture size to describe the track; a catalog that
// omits one is a publisher that is not ready.
func Test_packagerForTrack(t *testing.T) {
	w, h := int64(1280), int64(720)

	p, err := packagerForTrack(&msf.Track{
		Name: "video", Packaging: msf.PackagingLOC,
		Codec: "vp09.00.10.08", Width: &w, Height: &h,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, p.Init(), "the init segment comes from the catalog, not from media")

	_, err = packagerForTrack(&msf.Track{Name: "video", Codec: "vp09.00.10.08"})
	assert.Error(t, err, "no picture size")
}

// initFromTrack base64-decodes the catalog InitData (the fMP4 init), tolerating
// its absence or malformed values.
func Test_initFromTrack(t *testing.T) {
	want := []byte("fmp4-init-bytes")

	assert.Equal(t, want, initFromTrack(&msf.Track{InitData: base64.StdEncoding.EncodeToString(want)}))
	assert.Nil(t, initFromTrack(&msf.Track{}), "no InitData yields no init")
	assert.Nil(t, initFromTrack(&msf.Track{InitData: "!!!not-base64!!!"}), "malformed InitData yields no init")
}

// wallclockAt derives a group's wall-clock anchor from its media time, so the
// timeline a manifest publishes advances with the media rather than with
// whatever gap the network added between arrivals.
func Test_wallclockAt(t *testing.T) {
	anchor := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	const timescale = uint32(1_000_000) // microseconds

	tests := map[string]struct {
		mediaTime int64
		want      time.Time
	}{
		"at the anchor":    {mediaTime: 0, want: anchor},
		"one second in":    {mediaTime: 1_000_000, want: anchor.Add(time.Second)},
		"a fractional gap": {mediaTime: 1_006_500, want: anchor.Add(1_006_500 * time.Microsecond)},
		"an hour of media": {mediaTime: 3600 * 1_000_000, want: anchor.Add(time.Hour)},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, wallclockAt(anchor, tt.mediaTime, timescale))
		})
	}
}

// findTrack selects a track by name from the catalog.
func Test_findTrack(t *testing.T) {
	c := msf.Catalog{Tracks: []msf.Track{{Name: "video"}, {Name: "audio"}}}

	require.NotNil(t, findTrack(c, "video"))
	assert.Equal(t, "video", findTrack(c, "video").Name)
	assert.Nil(t, findTrack(c, "missing"))
}
