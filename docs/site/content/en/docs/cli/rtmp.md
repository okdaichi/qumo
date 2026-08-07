---
title: rtmp
description: Standalone RTMP ingest server that bridges published streams to MoQT.
weight: 2
---

Starts a standalone RTMP ingest server that bridges published streams to
MoQT. Unlike `qumo relay`, this does **not** participate in the peer mesh (no
peer connections, no announce relay) — it's a single self-contained origin.

```bash
qumo rtmp   # RTMP :1935 -> MoQT :4433
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTMP_INGEST_ADDR` | `:1935` | RTMP listen address. |
| `RTMP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins (default: same-origin only; `*` allows any). |

See [Deployment → Docker]({{< relref "../deployment/docker" >}}#demo-scenarios)
for a compose-based RTMP demo scenario.
