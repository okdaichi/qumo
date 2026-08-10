---
title: version
description: Print build-time version information.
weight: 9
---

Prints build-time version info. It doesn't run a server or connect to a
relay — purely local build metadata baked in at compile time.

## Usage

```
qumo version
```

Takes no flags or arguments. `qumo --version` and `qumo -v` are equivalent.

## Example

```console
$ qumo version
qumo dev
  commit: none
  built:  unknown
  go:     go1.26.5
```

The `dev`/`none`/`unknown` values above are what an unstamped local build
reports. Official binaries — from
[GitHub Releases](https://github.com/qumo-dev/qumo/releases) or the GHCR
images — embed the real version, commit, and build date.

## Configuration

None — `version` reads no environment variables.

## See also

- [Install]({{< relref "../install" >}}) — where the official, version-stamped binaries come from.
