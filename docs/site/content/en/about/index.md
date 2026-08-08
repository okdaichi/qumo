---
title: About
toc: false
description: qumo is an open-source Media over QUIC (MoQ) relay server with peer-based content discovery for distributed real-time media streaming.
---

## Project overview

`qumo` is a high-performance **Media over QUIC (MoQ)** relay server. It accepts
publishers and subscribers over QUIC/WebTransport, meshes with other relay
nodes for decentralized content discovery, and forwards live media with low
latency at scale.

It is built on top of [gomoqt](https://qumo-dev.github.io/gomoqt/), the
qumo-dev project's Go implementation of the MoQ protocol.

## Features

- **High-performance relay** — built on QUIC for low-latency media streaming
- **MoQT protocol** — full Media over QUIC Transport support (moq-lite draft-04)
- **Peer-based topology** — relays connect to each other via `ANNOUNCE_PLEASE` for decentralized content discovery
- **Observability** — Prometheus metrics, a health probe, and opt-in pprof profiling
- **TLS security** — built-in TLS 1.3 support, with optional mTLS between peers
- **Docker support** — env-var zero-config; prebuilt multi-arch images on GHCR (`ghcr.io/qumo-dev/qumo`)
- **RTMP/RTSP ingest** — bridge existing encoders and IP cameras into MoQT

## License

This project is licensed under the [Apache 2.0 License](https://github.com/qumo-dev/qumo/blob/main/LICENSE).

## Project links

{{<cards>}}
  {{<card link="https://github.com/qumo-dev/qumo" title="GitHub" icon="github" subtitle="Source, issues, and releases">}}
  {{<card link="https://github.com/qumo-dev/qumo/releases" title="Releases" icon="download" subtitle="Prebuilt binaries and Docker images">}}
  {{<card link="https://qumo-dev.github.io/gomoqt/" title="gomoqt" icon="external-link" subtitle="The Go MoQ protocol library qumo is built on">}}
{{</cards>}}
