---
title: rtsp
description: Pull from an RTSP source (e.g. an IP camera) and republish as MoQT.
weight: 3
---

Connects to an RTSP source (e.g. an IP camera), pulls the stream via
`DESCRIBE`/`SETUP`/`PLAY`, and republishes it as MoQT — with automatic
reconnect on failure.

```bash
qumo rtsp <rtsp-url> [broadcast-path]

# example
qumo rtsp rtsp://user:pass@192.168.1.50/stream1 /live/camera
```

- `broadcast-path` defaults to `/live/camera`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTSP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins. |

This is the **pull** direction (qumo dials the camera). For the **push**
direction (a camera or encoder dials qumo), see
[rtsp-push]({{< relref "rtsp-push" >}}).
