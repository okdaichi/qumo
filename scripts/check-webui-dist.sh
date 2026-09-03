#!/bin/sh
# Rebuild the playground web UI and verify the committed playground/dist
# matches byte-for-byte. Shared by ci.yml ("Web UI dist freshness") and
# release.yml (pre-GoReleaser gate) so the PR gate and the tag-time gate
# cannot drift apart. Requires deno on PATH plus the node version pinned in
# .node-version — the output must be reproducible, so an arbitrary node on
# PATH (a dev box, a new runner image) can produce bytes the gate rejects.
# Same build recipe as `mage webbuild` (magefiles/magefile.go).
#
# Why dist must be committed and fresh: Go module archives contain only
# git-tracked files, so `go install` embeds whatever dist is in git (#376).
# Vite's content-hashed output is deterministic for a locked dep set
# (deno.lock), so a fresh build must leave the tree byte-identical; any
# difference means playground/src (or the deps) changed without re-committing
# the dist.
set -eu

cd "$(dirname "$0")/../playground"
# --frozen: install exactly what deno.lock pins and fail if the lock is out
# of date (deps bumped in deno.json without re-locking), instead of silently
# resolving fresh versions and validating a dist built against unpinned deps.
deno install --frozen
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
