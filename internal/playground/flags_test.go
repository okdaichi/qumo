package playground

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags_Defaults(t *testing.T) {
	o, err := ParseFlags(nil, baseOpts())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", o.UIAddr)
	assert.Equal(t, "127.0.0.1:4433", o.RelayAddr)
}

func TestParseFlags_RelayAddrInjection(t *testing.T) {
	args := []string{"--relay-addr", "0.0.0.0:4433"}
	o, err := ParseFlags(args, baseOpts())
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:4433", o.RelayAddr)
	// UI defaults when not specified.
	assert.Equal(t, "127.0.0.1:8080", o.UIAddr)
}

func TestParseFlags_AllFlags(t *testing.T) {
	args := []string{
		"--ui-addr", "0.0.0.0:9000",
		"--relay-addr", "0.0.0.0:4433",
	}
	o, err := ParseFlags(args, baseOpts())
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:9000", o.UIAddr)
	assert.Equal(t, "0.0.0.0:4433", o.RelayAddr)
}

func TestParseFlags_HostFlagRemoved(t *testing.T) {
	// --host was removed in favor of per-request derivation; it must be rejected.
	_, err := ParseFlags([]string{"--host", "example.com"}, baseOpts())
	require.Error(t, err)
}

func TestParseFlags_RejectsUnknownPositional(t *testing.T) {
	_, err := ParseFlags([]string{"bogus"}, baseOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected argument")
}

func TestParseFlags_RejectsUnknownFlag(t *testing.T) {
	_, err := ParseFlags([]string{"--nope"}, baseOpts())
	require.Error(t, err)
	assert.False(t, IsErrHelp(err))
}

func TestParseFlags_HelpIsNotError(t *testing.T) {
	_, err := ParseFlags([]string{"-h"}, baseOpts())
	require.Error(t, err)
	assert.True(t, IsErrHelp(err))
}

func TestParseFlags_RequiresAssetsAndRelay(t *testing.T) {
	_, err := ParseFlags(nil, Options{StartRelay: func() error { return nil }})
	assert.ErrorContains(t, err, "Assets")

	_, err = ParseFlags(nil, Options{Assets: newTestAssets()})
	assert.ErrorContains(t, err, "StartRelay")
}

// baseOpts returns valid required Options for flag tests.
func baseOpts() Options {
	return Options{
		Assets:     newTestAssets(),
		StartRelay: func() error { return nil },
	}
}
