---
title: update
description: Update qumo in place to the latest GitHub release.
weight: 9
---

Replaces the running binary with the latest release from
[GitHub Releases](https://github.com/qumo-dev/qumo/releases), verifying the
download's SHA-256 against the release's `checksums.txt` before swapping it in.
It picks the archive for your OS and architecture automatically (`tar.gz` on
Linux/macOS, `.zip` on Windows).

## Usage

```
qumo update [--check]
```

| Flag | Default | Description |
|---|---|---|
| `--check` | `false` | Report whether a newer release exists and exit, without touching the binary. |

## Example

```console
$ qumo update --check
qumo v0.6.260906 is available (current: v0.6.260903)

$ qumo update
qumo: updating v0.6.260903 → v0.6.260906 ...
qumo: updated to v0.6.260906
```

When you are already current, both forms say so and exit 0:

```console
$ qumo update --check
qumo v0.6.260906 is already up to date
```

## Development builds

A binary built from a source checkout reports its version as `dev`, which has
no meaningful ordering against a release tag. Rather than guess, `update`
declines:

```console
$ qumo update
qumo: dev build — skipping update check
```

Rebuild from source (or install a release binary) to get updates. This is why
`update` is a no-op in a development tree.

## Versioning

qumo tags releases as `v<major>.<minor>.<YYMMDD>` — SemCalVer, where the last
component is the release date rather than a patch number. `update` compares
both that form and plain SemVer. A stable build upgrades only to stable
releases; a pre-release build also considers pre-releases, taking the newest
non-draft release of either kind.

## Configuration

None — `update` reads no environment variables.

## See also

- [version]({{< relref "version" >}}) — what the current binary reports.
- [Install]({{< relref "../install" >}}) — the install paths `update` upgrades between.
