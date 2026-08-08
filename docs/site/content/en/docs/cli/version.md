---
title: version
description: Print build-time version information.
weight: 8
---

Prints build-time version info. It doesn't run a server or connect to a
relay — purely local build metadata baked in at compile time.

```bash
qumo version   # equivalent: qumo --version / qumo -v
```

```
qumo dev
  commit: none
  built:  unknown
  go:     go1.26.5
```

The `dev`/`none`/`unknown` values above are what an unstamped local build
reports. Official binaries — from
[GitHub Releases](https://github.com/qumo-dev/qumo/releases) or the GHCR
images — embed the real version, commit, and build date.
