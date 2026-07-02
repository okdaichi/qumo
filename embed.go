package main

import "embed"

// playgroundAssets embeds the built playground UI. The directive lives in
// package main (repo root) rather than internal/playground because go:embed
// paths are relative to the .go file and cannot traverse with ".."; the
// playground/dist tree is only reachable from the repo root. The dist directory
// is populated by `mage webbuild` (Vite); a placeholder index.html is committed
// so the embed always matches even on a fresh clone.
//
//go:embed all:playground/dist
var playgroundAssets embed.FS
