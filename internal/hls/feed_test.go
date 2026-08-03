package hls

import (
	"testing"
	"time"

	"github.com/okdaichi/qumo-ledger/ledger"
	"github.com/stretchr/testify/assert"
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
