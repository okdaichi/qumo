#!/bin/sh
# Rebuild the playground web UI and verify the committed playground/dist
# matches byte-for-byte. Shared by ci.yml ("Web UI dist freshness") and
# release.yml (pre-GoReleaser gate) so the PR gate and the tag-time gate
# cannot drift apart. Requires deno on PATH (node for Vite is assumed to be
# preinstalled — same recipe as `mage webbuild`, magefiles/magefile.go).
#
# Why dist must be committed and fresh: Go module archives contain only
# git-tracked files, so `go install` embeds whatever dist is in git (#376).
# Vite's content-hashed output is deterministic for a locked dep set
# (deno.lock), so a fresh build must leave the tree byte-identical; any
# difference means playground/src (or the deps) changed without re-committing
# the dist.
set -eu

cd "$(dirname "$0")/../playground"
deno install
deno task build
cd ..

if ! git diff --exit-code -- playground/dist; then
	echo "::error::playground/dist is stale — run 'mage webbuild' and commit the result (go install embeds the committed dist, see #376)"
	exit 1
fi
if [ -n "$(git status --porcelain playground/dist)" ]; then
	git status --porcelain playground/dist
	echo "::error::playground/dist has uncommitted build output — commit the rebuilt dist (go install embeds the committed dist, see #376)"
	exit 1
fi
