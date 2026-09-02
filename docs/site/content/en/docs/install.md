---
title: Install
description: Install qumo via Go, prebuilt binary, Docker, or build from source.
weight: 1
---

qumo ships as a single static binary. Pick whichever install path fits —
these are alternatives, not sequential steps.

{{< tabs >}}

{{< tab name="Go install" >}}
```bash
go install github.com/qumo-dev/qumo@latest
```
{{< /tab >}}

{{< tab name="Binary release" >}}
Download the latest archive from [GitHub Releases](https://github.com/qumo-dev/qumo/releases):

```bash
# Linux/macOS (replace 0.5.0 with the latest version from the releases page)
curl -L https://github.com/qumo-dev/qumo/releases/download/v0.5.0/qumo_0.5.0_linux_amd64.tar.gz | tar xz
./qumo playground      # one-command demo: relay + web UI at http://127.0.0.1:8080

# Windows: download qumo_0.5.0_windows_amd64.zip from the releases page
```
{{< /tab >}}

{{< tab name="Docker" >}}
Prebuilt multi-arch images are published to GHCR — see
[Deployment → Docker]({{< relref "deployment/docker" >}}) for the pull/run
commands and compose examples.
{{< /tab >}}

{{< tab name="Build from source" >}}
```bash
git clone https://github.com/qumo-dev/qumo.git
cd qumo
mage build        # builds bin/qumo with version info
# or: go build -o qumo .
```

Requirements for building from source:

- **Go 1.27+**
- **Deno** — the binary embeds a web UI built by Deno + Vite (`mage build`)
- **Mage** — build automation: `go install github.com/magefile/mage@latest`
{{< /tab >}}

{{< /tabs >}}

## Verify

```bash
qumo version
```

## Next

Once installed, see [Configuration]({{< relref "configuration" >}}) to set up
TLS and environment variables, then start a relay:

```bash
qumo relay
```
