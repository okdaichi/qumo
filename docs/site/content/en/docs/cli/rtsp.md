---
title: rtsp
description: Pull from an RTSP source (e.g. an IP camera) and republish as MoQT.
weight: 3
---

Connects to an RTSP source (e.g. an IP camera), pulls the stream via
`DESCRIBE`/`SETUP`/`PLAY`, and republishes it as MoQT — with automatic
reconnect on failure. Like [rtmp]({{< relref "rtmp" >}}) and
[rtsp-push]({{< relref "rtsp-push" >}}), this is a self-contained origin and
does not join the relay peer mesh.

## Usage

```
qumo rtsp <rtsp-url> [broadcast-path]
```

| Argument | Default | Description |
|---|---|---|
| `<rtsp-url>` | (required) | Source to pull from — `rtsp://[user:pass@]host/path`. |
| `[broadcast-path]` | `/live/camera` | MoQT broadcast path to republish on. |

## Example

```console
$ qumo rtsp rtsp://user:pass@192.168.1.50/stream1 /live/camera
INFO ingest session started broadcast_path=/live/camera
INFO RTSP pull ingest starting source=rtsp://user:pass@192.168.1.50/stream1 broadcast_path=/live/camera serve=:4433
```

Subscribers then consume `/live/camera` over MoQT on `:4433`. If the source
is unreachable or drops, it logs the failure and retries with a doubling
backoff rather than exiting:

```console
WARN RTSP pull disconnected, reconnecting error="dial: rtsp: dial 192.168.1.50:554: ..." backoff=2s
WARN RTSP pull disconnected, reconnecting error="dial: rtsp: dial 192.168.1.50:554: ..." backoff=4s
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTSP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins (default: same-origin only; `*` allows any). |

## See also

- [rtsp-push]({{< relref "rtsp-push" >}}) — the **push** direction, where the camera dials qumo instead.
- [rtmp]({{< relref "rtmp" >}}) — the RTMP equivalent of this command.
- [Deployment → Docker]({{< relref "../deployment/docker" >}}) — running it as a container.
- [Deployment → TLS & mTLS]({{< relref "../deployment/tls" >}}) — generating the certificate it needs.
