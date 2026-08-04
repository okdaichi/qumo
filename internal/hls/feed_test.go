package hls

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/msf"

	"github.com/okdaichi/qumo-ledger/ledger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groupInfo derives a monotonic media time from the append ordinal — not the
// producer sequence — so a dropped MoQ group (a gappy sequence) does not advance
// the timeline. The sequence is the group's identity; the epoch is stamped by
// the writer.
func Test_groupInfo(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	const du int64 = 180000 // two seconds at 90 kHz

	tests := map[string]struct {
		seq, index int64
		wantMedia  int64
	}{
		"first group":  {seq: 5, index: 0, wantMedia: 0},
		"second group": {seq: 6, index: 1, wantMedia: 180000},
		"gappy seq":    {seq: 99, index: 2, wantMedia: 360000},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := groupInfo(uint64(tt.seq), tt.index, du, 30, now)

			assert.Equal(t, ledger.NewGroupID(0, uint64(tt.seq)), got.ID,
				"the producer sequence is the identity; the epoch is stamped by the writer")
			assert.Equal(t, tt.wantMedia, got.MediaTime,
				"media time follows the append ordinal, not the gappy producer sequence")
			assert.Equal(t, du, got.Duration)
			assert.Equal(t, now.UnixNano(), got.Wallclock)
			assert.Equal(t, uint64(30), got.ObjectCount)
		})
	}
}

// schemaFromTrack projects a CMAF catalog track onto the ledger schema, taking
// the timescale and MIME from the catalog and falling back when they are absent.
func Test_schemaFromTrack(t *testing.T) {
	t.Run("from catalog", func(t *testing.T) {
		ts := int64(90000)
		s := schemaFromTrack(&msf.Track{
			Name: "video", Packaging: msf.PackagingCMAF,
			Timescale: &ts, MimeType: "video/mp4", Codec: "avc1",
		}, 1000)

		assert.Equal(t, uint32(90000), s.Timescale)
		assert.Equal(t, "video/mp4", s.MIME)
		assert.Equal(t, "fmp4", s.Encoding)
		assert.Equal(t, ledger.TimeSourceIngest, s.TimeSource)
	})

	t.Run("fallback timescale", func(t *testing.T) {
		s := schemaFromTrack(&msf.Track{Name: "video", Packaging: msf.PackagingCMAF}, 1000)
		assert.Equal(t, uint32(1000), s.Timescale)
		assert.Equal(t, "video/mp4", s.MIME, "MIME defaults to video/mp4 for CMAF video")
	})
}

// initFromTrack base64-decodes the catalog InitData (the fMP4 init), tolerating
// its absence or malformed values.
func Test_initFromTrack(t *testing.T) {
	want := []byte("fmp4-init-bytes")

	assert.Equal(t, want, initFromTrack(&msf.Track{InitData: base64.StdEncoding.EncodeToString(want)}))
	assert.Nil(t, initFromTrack(&msf.Track{}), "no InitData yields no init")
	assert.Nil(t, initFromTrack(&msf.Track{InitData: "!!!not-base64!!!"}), "malformed InitData yields no init")
}

// findTrack selects a track by name from the catalog.
func Test_findTrack(t *testing.T) {
	c := msf.Catalog{Tracks: []msf.Track{{Name: "video"}, {Name: "audio"}}}

	require.NotNil(t, findTrack(c, "video"))
	assert.Equal(t, "video", findTrack(c, "video").Name)
	assert.Nil(t, findTrack(c, "missing"))
}
