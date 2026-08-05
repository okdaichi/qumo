---
title: version
description: Print build-time version information.
weight: 8
---

```bash
qumo version
# equivalent: qumo --version / qumo -v
```

```
qumo dev
  commit: none
  built:  unknown
  go:     go1.26.5
```

Release builds (`mage build`, or the binaries on
[GitHub Releases](https://github.com/qumo-dev/qumo/releases)) embed the real
version, commit, and build date instead of `dev`/`none`/`unknown`.
