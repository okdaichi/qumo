package rtsp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInterleaved(t *testing.T) {
	tests := map[string]struct {
		transport string
		rtp       uint8
		rtcp      uint8
		ok        bool
	}{
		"pair":      {"RTP/AVP/TCP;unicast;interleaved=0-1", 0, 1, true},
		"pair 2":    {"interleaved=2-3", 2, 3, true},
		"single":    {"interleaved=4", 4, 4, true},
		"with mode": {"RTP/AVP/TCP;unicast;mode=PLAY;interleaved=6-7", 6, 7, true},
		"absent":    {"RTP/AVP;unicast", 0, 0, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rtp, rtcp, ok := parseInterleaved(tc.transport)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.rtp, rtp)
				assert.Equal(t, tc.rtcp, rtcp)
			}
		})
	}
}

func TestSelectQop(t *testing.T) {
	assert.Equal(t, "auth", selectQop("auth"))
	assert.Equal(t, "auth", selectQop("auth,auth-int"))
	assert.Equal(t, "auth", selectQop("auth-int,auth"))
	assert.Equal(t, "", selectQop("auth-int")) // not supported → legacy
	assert.Equal(t, "", selectQop(""))         // no qop
}

func TestDial_InvalidScheme(t *testing.T) {
	_, err := Dial(context.Background(), "http://example.com/stream")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an rtsp url")
}

func TestDial_MalformedURL(t *testing.T) {
	_, err := Dial(context.Background(), "://bad")
	require.Error(t, err)
}
