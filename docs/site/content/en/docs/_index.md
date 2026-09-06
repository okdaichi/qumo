---
linkTitle: "Documentation"
title: Introduction
description: Complete documentation for qumo — install, configure, deploy, and operate a Media over QUIC relay.
cascade:
  type: docs
---

Welcome to the qumo docs. qumo is a relay server, so most of what's here is
aimed at **running and operating** it — start with Install, then Configuration.

{{< cards >}}
	{{< card link="install" title="Install" icon="download" subtitle="Go install, binary release, Docker, or build from source." >}}
	{{< card link="configuration" title="Configuration" icon="adjustments" subtitle="Environment variables, TLS, and the qumo doctor command." >}}
{{< /cards >}}

## Deployment

Once qumo is running, this covers how relays mesh together and stay reachable
in production — Docker topologies, peer discovery, Nomad, and TLS/mTLS:

{{< cards >}}
	{{< card link="deployment" title="Deployment" icon="globe-alt" subtitle="Docker topologies, peer discovery, Nomad, and TLS for deploying qumo relays." >}}
{{< /cards >}}

## Operate

{{< cards >}}
	{{< card link="observability" title="Observability" icon="chart-bar" subtitle="Prometheus metrics, health checks, and pprof." >}}
{{< /cards >}}

## Reference

{{< cards >}}
	{{< card link="cli" title="CLI reference" icon="terminal" subtitle="relay, rtmp, rtsp, rtsp-push, hls, playground, doctor, loadgen, update, version." >}}
{{< /cards >}}

## Feedback

If you find any mistakes, gaps, or would like to contribute improvements,
please open an Issue or Pull Request on [GitHub](https://github.com/qumo-dev/qumo).
