---
title: hls
description: Start the HLS/DASH egress server.
weight: 5
---

The HLS/DASH egress: subscribes to a MoQ track's catalog to learn its schema and
fMP4 init segment, writes each received CMAF group into a qumo-ledger track, and
serves the ledger's HLS and DASH renderings over HTTP. It is a separate process
from the relay and the publisher — run it alongside a relay that a CMAF
publisher (the playground's HLS scenario, or `cmd/seed-moq`) is publishing to.

It verifies the relay's certificate by default.

## Usage

```
qumo hls
```

No flags — it is configured entirely through environment variables.

## Example

Subscribe to a relay and serve on the default address:

```console
$ qumo hls
INFO hls: serving addr=:8080 track=live/cam1/video
```

The egress serves both formats from its HTTP root, routing by the request URL's
base name:

```console
$ curl http://localhost:8080/playlist.m3u8   # HLS media playlist
$ curl http://localhost:8080/manifest.mpd     # DASH MPD
```

Segments are shared by both formats and addressed by group id
(`http://localhost:8080/<group-id>.m4s`); the fMP4 init segment is served at
`/init.m4s` and referenced from the playlist's `#EXT-X-MAP`. Point a player at
the playlist URL — `hls.js` for HLS in a browser, or any HLS/DASH-capable
player. If the playground is running (its UI also binds `:8080`), set
`HLS_ADDR` to another port.

## TLS

The egress verifies the relay's certificate against the system root store by
default. Trust a specific relay cert with `RELAY_CA_FILE`, or disable
verification for a self-signed dev relay with `RELAY_TLS_INSECURE=true`:

```bash
qumo hls                                   # verify against the system roots
RELAY_CA_FILE=certs/server.crt qumo hls    # trust this relay's cert
RELAY_TLS_INSECURE=true qumo hls           # dev relay with a self-signed cert
```

`cmd/seed-moq`, the dev seeder, presents an ephemeral self-signed certificate —
point the egress at it with `RELAY_TLS_INSECURE=true`.

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `HLS_ADDR` | `:8080` | HTTP listen address. |
| `RELAY_URL` | `https://localhost:4433` | MoQ relay URL to subscribe to. |
| `RELAY_TRACK_PATH` | `/hls/live` | MoQ broadcast path whose catalog to read. |
| `RELAY_TRACK_NAME` | `video` | Media track name in the catalog to relay. |
| `LEDGER_ROOT` | `./ledger` | qumo-ledger filesystem store directory. |
| `LEDGER_TRACK` | `live/cam1/video` | Ledger track path to write groups into. |
| `HLS_WINDOW` | `12` | Segments kept in the manifest — the live window, and how far back a viewer can seek. `0` lists the whole track, making it a recording rather than a live stream. |
| `HLS_LIVE_TIMEOUT_S` | `10` | Seconds of silence after which the publisher is treated as gone; the feed reconnects and manifests answer `503`. |
| `RELAY_CA_FILE` | _unset_ | PEM cert to trust as the relay's root, overriding the system roots. Unset means verify against the system root store. |
| `RELAY_TLS_INSECURE` | `false` | Skip relay TLS verification entirely. Dominates `RELAY_CA_FILE` when both are set. |
| `CORS_ALLOWED_ORIGINS` | _unset_ | Comma-separated origins allowed to fetch manifests and segments, or `*` for any. Unset disables CORS. Required when the player is served from another origin (e.g. `http://localhost:5173` for the playground). |

## See also

- [relay]({{< relref "relay" >}}) — the MoQ relay the egress subscribes to.
- [playground]({{< relref "playground" >}}) — the local demo whose HLS scenario publishes CMAF for the egress to serve.
- [Configuration]({{< relref "../configuration" >}}) — the shared environment variable reference.
- [Deployment → TLS & mTLS]({{< relref "../deployment/tls" >}}) — generating the certificate the relay presents.
