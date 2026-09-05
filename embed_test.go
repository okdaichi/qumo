package main

import (
	"io/fs"
	"testing"

	"github.com/qumo-dev/qumo/internal/playground"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlaygroundAssets_Embedded guards the actual //go:embed tree, which the
// internal/playground unit tests cannot reach: they exercise VerifyAssets
// against fstest fixtures, so a checkout (or a tag) whose playground/dist lost
// its bundles still compiles and passes the whole suite while shipping a
// white-screening binary (#376).
//
// This is the cheap half of the guarantee: it needs no deno, no node, and no
// byte-exactness, so it runs on every `go test ./...`. CI's "Web UI dist
// freshness" job is the expensive half that proves the committed bundles are
// also up to date with playground/src.
func TestPlaygroundAssets_Embedded(t *testing.T) {
	assets := mustSubAssets()

	require.NoError(t, playground.VerifyAssets(assets))

	// VerifyAssets only checks the refs index.html actually makes, so an
	// index.html with no /assets/ refs at all would satisfy it vacuously.
	// Every real Vite build emits at least the entry chunk.
	bundles, err := fs.ReadDir(assets, "assets")
	require.NoError(t, err)
	assert.NotEmpty(t, bundles)
}
