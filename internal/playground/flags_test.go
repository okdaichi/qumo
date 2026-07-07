package playground

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		base      Options
		want      Options
		wantErr   bool
		errString string
		errHelp   bool
	}{
		{
			name: "defaults",
			args: nil,
			base: baseOpts(),
			want: Options{
				UIAddr:    "127.0.0.1:8080",
				RelayAddr: "127.0.0.1:4433",
			},
		},
		{
			name: "set ui addr",
			args: []string{"--ui-addr", "0.0.0.0:80"},
			base: baseOpts(),
			want: Options{
				UIAddr:    "0.0.0.0:80",
				RelayAddr: "127.0.0.1:4433",
			},
		},
		{
			name: "set relay addr",
			args: []string{"--relay-addr", "0.0.0.0:443"},
			base: baseOpts(),
			want: Options{
				UIAddr:    "127.0.0.1:8080",
				RelayAddr: "0.0.0.0:443",
			},
		},
		{
			name: "set both addrs",
			args: []string{"--ui-addr", "0.0.0.0:80", "--relay-addr", "0.0.0.0:443"},
			base: baseOpts(),
			want: Options{
				UIAddr:    "0.0.0.0:80",
				RelayAddr: "0.0.0.0:443",
			},
		},
		{
			name:    "host flag removed",
			args:    []string{"--host", "example.com"},
			base:    baseOpts(),
			wantErr: true,
		},
		{
			name:      "missing assets",
			args:      nil,
			base:      Options{StartRelay: func() error { return nil }},
			wantErr:   true,
			errString: "playground: Assets is required",
		},
		{
			name:      "missing start relay",
			args:      nil,
			base:      Options{Assets: newTestAssets()},
			wantErr:   true,
			errString: "playground: StartRelay is required",
		},
		{
			name:    "unknown flag",
			args:    []string{"--nope"},
			base:    baseOpts(),
			wantErr: true,
		},
		{
			name:      "unknown positional arg",
			args:      []string{"bogus"},
			base:      baseOpts(),
			wantErr:   true,
			errString: "unexpected argument: bogus",
		},
		{
			name:    "help flag",
			args:    []string{"-h"},
			base:    baseOpts(),
			wantErr: true,
			errHelp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFlags(tt.args, tt.base)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errString != "" {
					assert.Contains(t, err.Error(), tt.errString)
				}
				if tt.errHelp {
					assert.True(t, IsErrHelp(err))
				} else {
					assert.False(t, IsErrHelp(err))
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want.UIAddr, got.UIAddr)
			assert.Equal(t, tt.want.RelayAddr, got.RelayAddr)
		})
	}
}

// baseOpts returns valid required Options for flag tests.
func baseOpts() Options {
	return Options{
		Assets:     newTestAssets(),
		StartRelay: func() error { return nil },
	}
}

func TestUsageHelp(t *testing.T) {
	var buf bytes.Buffer
	UsageHelp(&buf)
	out := buf.String()
	assert.Contains(t, out, "Usage: qumo playground [flags]")
	assert.Contains(t, out, "--ui-addr <addr>")
	assert.Contains(t, out, "--relay-addr <addr>")
}
