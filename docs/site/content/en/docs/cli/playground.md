---
title: playground
description: Start a local demo — in-process relay plus the embedded web UI.
weight: 6
---

Starts a self-contained local demo: an in-process relay plus the embedded web
UI. It's single-node only — `PEERS` is cleared on startup so it can't be
pulled into a peer mesh even if you've set one in your environment.

## Usage

```
qumo playground [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--ui-addr <addr>` | `127.0.0.1:8080` | UI HTTP bind address. |
| `--relay-addr <addr>` | `127.0.0.1:4433` | Relay WebTransport bind address. |

The browser learns the relay URL automatically from whatever host it opened
the UI at, so there's no `--host` flag.

## Example

```console
$ qumo playground
INFO dev certificate ready cert=...\qumo\playground\server.crt hash=fc3f2696d19e8be0...
INFO playground ready url=http://127.0.0.1:8080 relay_addr=127.0.0.1:4433 note="relayUrl is derived per-request from the browser's Host"
http://127.0.0.1:8080
INFO relay: UDP receive buffer size="default 262144 (256 KB)"
	Host    : 127.0.0.1:4433
	Node ID : playground
	/       : WebTransport endpoint
	/health : health probe
	/metrics: Prometheus metrics
```

The bare URL on its own line is deliberate — it's the one thing you need, so
it's easy to click or copy out of the log.

Behind your own TLS-terminating reverse proxy, bind the relay publicly and
proxy the UI:

```bash
qumo playground --relay-addr 0.0.0.0:4433
# proxy https://example.com -> 127.0.0.1:8080; relay UDP/4433 reachable directly.
# The UI must be HTTPS: WebTransport requires a secure context (localhost excepted).
# /config returns relayUrl=https://example.com:4433 (derived from the proxy's Host).
```

## Configuration

No environment variables of its own — the two flags above are the entire
surface. It generates its own dev certificate and sets the underlying relay's
`RELAY_ADDR`/`CERT_FILE`/`KEY_FILE` automatically.

## See also

- [relay]({{< relref "relay" >}}) — a standalone relay without the embedded UI.
- [Configuration]({{< relref "../configuration" >}}) — what the variables it sets internally mean.
