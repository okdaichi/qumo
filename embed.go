package main

import "embed"

// playgroundAssets embeds the built playground UI. The directive lives in
// package main (repo root) rather than internal/playground because go:embed
// paths are relative to the .go file and cannot traverse with ".."; the
// playground/dist tree is only reachable from the repo root. The dist directory
// is produced by `mage webbuild` (Vite) and COMMITTED so that `go install`
// embeds the real UI — module zips contain only git-tracked files. CI keeps it
// fresh (ci.yml "Web UI dist freshness"). As a safety net for checkouts that
// somehow lack the bundles, `qumo playground` detects a bundle-less dist at
// runtime (internal/playground.VerifyAssets) and warns + serves an explanatory
// page instead of the broken UI.
//
//go:embed all:playground/dist
var playgroundAssets embed.FS
