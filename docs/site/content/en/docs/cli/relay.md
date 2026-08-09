---
title: relay
description: Start the MoQT relay server.
weight: 1
---

The core `qumo` command: accepts publishers and subscribers over
QUIC/WebTransport, and meshes with peer relays for content discovery.

## Usage

```
qumo relay [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--role <hub\|edge>` | flat / single-node | Node topology role. Everything else is configured via environment variables. |

## Example

```bash
qumo relay                # standalone / flat relay
qumo relay --role hub     # hub node — discovers no local peers
qumo relay --role edge    # edge node — discovers local hubs
```

It needs a TLS certificate to bind at all — `CERT_FILE`/`KEY_FILE`, default
`certs/server.crt`/`certs/server.key` — or it fails immediately with a
"failed to load X509 key pair" error. Once it's running:

```bash
curl http://localhost:4433/health
```

## Configuration

Everything other than `--role` is an environment variable: bind address, TLS
certificates, peer discovery, capacity tuning, and credential auth. Unlike
the ingest commands, the relay's surface is large enough to have its own
page — see [Configuration]({{< relref "../configuration" >}}) for the full
reference.

## See also

- [Configuration]({{< relref "../configuration" >}}) — the full environment variable reference.
- [Deployment → Peer topology]({{< relref "../deployment/peer-topology" >}}) — how `--role` fits into peer discovery.
- [Deployment → TLS & mTLS]({{< relref "../deployment/tls" >}}) — generating the certificate it needs.
- [Deployment → Docker]({{< relref "../deployment/docker" >}}) — running it as a container.
- [Observability]({{< relref "../observability" >}}) — `/health`, `/metrics`, and `qumo doctor`.
