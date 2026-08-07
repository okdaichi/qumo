---
title: rtsp-push
description: Standalone RTSP push ingest server (ANNOUNCE/RECORD) that bridges published streams to MoQT.
weight: 4
---

Starts a standalone RTSP **push** ingest server (`ANNOUNCE`/`RECORD`) that
bridges published streams to MoQT. Like [rtmp]({{< relref "rtmp" >}}) and
[rtsp]({{< relref "rtsp" >}}), this is a self-contained origin and does not
join the relay peer mesh.

```
Usage: qumo rtsp-push
```

```bash
qumo rtsp-push   # RTSP :8554 -> MoQT :4433
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTSP_INGEST_ADDR` | `:8554` | RTSP listen address. |
| `RTSP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins (default: same-origin only; `*` allows any). |

This is the **push** direction (a camera or encoder dials qumo). For the
**pull** direction (qumo dials the camera), see [rtsp]({{< relref "rtsp" >}}).
