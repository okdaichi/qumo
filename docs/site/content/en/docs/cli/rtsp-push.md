---
title: rtsp-push
description: Start the RTSP push ingest server (ANNOUNCE/RECORD), bridging published streams to MoQT.
weight: 4
---

Starts a standalone RTSP **push** ingest server (`ANNOUNCE`/`RECORD`) that
bridges published streams to MoQT. Like [rtmp]({{< relref "rtmp" >}}) and
[rtsp]({{< relref "rtsp" >}}), this is a self-contained origin and does not
join the relay peer mesh.

## Usage

```
qumo rtsp-push
```

Takes no flags or arguments — configured entirely through environment
variables.

## Example

```console
$ qumo rtsp-push
	Ingest  : :8554
	Serve   : :4433
```

Encoders then `ANNOUNCE`/`RECORD` to `rtsp://<host>:8554/<path>`, and
subscribers consume the same path over MoQT on `:4433`. Override either
address to taste:

```console
$ RTSP_INGEST_ADDR=:8555 RTSP_SERVE_ADDR=127.0.0.1:4457 qumo rtsp-push
	Ingest  : :8555
	Serve   : 127.0.0.1:4457
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `RTSP_INGEST_ADDR` | `:8554` | RTSP listen address. |
| `RTSP_SERVE_ADDR` | `:4433` | MoQT listen address. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `CORS_ALLOWED_ORIGINS` | (unset) | Comma-separated WebTransport origins (default: same-origin only; `*` allows any). |

## See also

- [rtsp]({{< relref "rtsp" >}}) — the **pull** direction, where qumo dials the camera instead.
- [rtmp]({{< relref "rtmp" >}}) — the RTMP equivalent of this command.
- [Deployment → Docker]({{< relref "../deployment/docker" >}}) — running it as a container.
- [Deployment → TLS & mTLS]({{< relref "../deployment/tls" >}}) — generating the certificate it needs.
