---
title: rtmp
description: Start the RTMP ingest server, bridging published streams to MoQT.
weight: 2
---

Starts a standalone RTMP ingest server that bridges published streams to
MoQT. Like [rtsp]({{< relref "rtsp" >}}) and [rtsp-push]({{< relref "rtsp-push" >}}),
this is a self-contained origin and does not join the relay peer mesh (no
peer connections, no announce relay).

## Usage

```
qumo rtmp
```

Takes no flags or arguments — configured entirely through environment
variables.

## Example

```bash
qumo rtmp                          # RTMP :1935 -> MoQT :4433

RTMP_INGEST_ADDR=:1936 qumo rtmp   # listen for RTMP on a different port
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTMP_INGEST_ADDR` | `:1935` | RTMP listen address. |
| `RTMP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins (default: same-origin only; `*` allows any). |

## See also

- [rtsp]({{< relref "rtsp" >}}) — pull an RTSP source into MoQT.
- [rtsp-push]({{< relref "rtsp-push" >}}) — the RTSP equivalent of this command.
- [Deployment → Docker]({{< relref "../deployment/docker" >}}) — running it as a container.
- [Deployment → TLS & mTLS]({{< relref "../deployment/tls" >}}) — generating the certificate it needs.
