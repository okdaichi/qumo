package playground

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyAssets(t *testing.T) {
	tests := map[string]struct {
		assets  fstest.MapFS
		wantErr bool
	}{
		"bundled UI with hashed assets": {
			assets: fstest.MapFS{
				"index.html":                {Data: []byte(`<script type="module" src="/assets/index-4RJU1QOf.js"></script><link rel="stylesheet" href="/assets/index-0pFl8nQc.css">`)},
				"assets/index-4RJU1QOf.js":  {Data: []byte("app")},
				"assets/index-0pFl8nQc.css": {Data: []byte("body{}")},
			},
		},
		"placeholder dist referencing missing bundles": {
			// The committed placeholder index.html references hashed assets
			// that only exist after a Vite build (#376).
			assets: fstest.MapFS{
				"index.html": {Data: []byte(`<link rel="icon" href="/vite.svg"><script type="module" src="/assets/index-4RJU1QOf.js"></script>`)},
			},
			wantErr: true,
		},
		"missing non-bundle reference alone does not trip": {
			// The check is scoped to /assets/ bundle refs (see assets.go): a
			// missing favicon or other public/ file must not flip a fully-built
			// dist to the error page.
			assets: fstest.MapFS{
				"index.html":    {Data: []byte(`<link rel="icon" href="/vite.svg"><script type="module" src="/assets/app.js"></script>`)},
				"assets/app.js": {Data: []byte("app")},
			},
		},
		"query strings and fragments are stripped before stat": {
			assets: fstest.MapFS{
				"index.html":    {Data: []byte(`<script type="module" src="/assets/app.js?v=1#h"></script>`)},
				"assets/app.js": {Data: []byte("app")},
			},
		},
		"attribute-name suffixes are not matched": {
			// data-src / xlink:href must not look like bundle references.
			assets: fstest.MapFS{
				"index.html": {Data: []byte(`<img data-src="/assets/lazy.js" xlink:href="/assets/x.js">`)},
			},
		},
		"inlined build with no external references": {
			assets: fstest.MapFS{
				"index.html": {Data: []byte(`<!doctype html><title>qumo</title>`)},
			},
		},
		"external and data URLs are ignored": {
			assets: fstest.MapFS{
				"index.html": {Data: []byte(`<link rel="preconnect" href="https://example.com"><img src="data:image/png;base64,xxxx">`)},
			},
		},
		"missing index.html": {
			assets:  fstest.MapFS{"assets/app.js": {Data: []byte("app")}},
			wantErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := verifyAssets(tt.assets)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestVerifyAssets_ErrorsListMissingFilesAndFix(t *testing.T) {
	// The error is user-facing (surfaced as a startup warning); it must name
	// the missing files and tell the user how to build the bundles.
	err := verifyAssets(fstest.MapFS{
		"index.html": {Data: []byte(`<script src="/assets/missing.js"></script>`)},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "/assets/missing.js")
	assert.Contains(t, err.Error(), buildAssetsHint)
}
