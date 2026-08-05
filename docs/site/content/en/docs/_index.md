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

Once qumo is running, these cover how relays mesh together and stay reachable
in production:

{{< cards >}}
	{{< card link="deployment/docker" title="Docker" icon="cube" subtitle="Compose files for single-relay and multi-region topologies." >}}
	{{< card link="deployment/peer-topology" title="Peer topology" icon="globe-alt" subtitle="How relays discover and mesh with each other." >}}
	{{< card link="deployment/nomad" title="Nomad" icon="server" subtitle="Cluster-native peer discovery via the Nomad service API." >}}
	{{< card link="deployment/tls" title="TLS & mTLS" icon="lock-closed" subtitle="Certificates, and mutual TLS between peers." >}}
{{< /cards >}}

## Operating

{{< cards >}}
	{{< card link="observability" title="Observability" icon="chart-bar" subtitle="Prometheus metrics, health checks, and pprof." >}}
	{{< card link="cli" title="CLI reference" icon="terminal" subtitle="relay, rtmp, rtsp, rtsp-push, playground, loadgen, doctor." >}}
{{< /cards >}}

## Feedback 📋

If you find any mistakes, gaps, or would like to contribute improvements,
please open an Issue or Pull Request on [GitHub](https://github.com/qumo-dev/qumo).
