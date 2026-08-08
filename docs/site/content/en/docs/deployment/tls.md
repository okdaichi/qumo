---
title: TLS & mTLS
description: Certificates for the relay, and mutual TLS between peer relays.
weight: 4
---

## Server TLS

qumo requires TLS 1.3 for its QUIC listener.

| Variable | Default | Description |
|---|---|---|
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. |
| `INSECURE` | `false` | Generate an ephemeral self-signed cert (dev/test only). |

For local development, `mage cert` generates a dev cert (mkcert if available,
otherwise self-signed):

```bash
mage cert
qumo relay
```

## Mutual TLS between peers (optional)

Setting `CA_FILE` enables mutual TLS for the relay's peer mesh:

- incoming peer connections that present a client cert are verified against this CA;
- the dialer presents this node's `CERT_FILE` cert to remote relays and trusts only the CA pool;
- remote resolver clients also present the client cert and verify the remote server against this CA (when `REMOTE_TLS_ENABLED=true`).

Connections **without** a client cert are still allowed by default, so
browser/WebTransport clients keep working unmodified.

| Variable | Default | Description |
|---|---|---|
| `CA_FILE` | (unset) | PEM CA certificate. Leave unset to disable mTLS entirely. |
| `MTLS_REQUIRED` | `false` | When `true`, every connection must present a client cert signed by `CA_FILE`. Use this for relay-only clusters with no direct browser traffic. |

## Remote resolver TLS

`REMOTE_TLS_ENABLED=true` enables mTLS specifically for the connection to a
remote traffic resolver, reusing `CERT_FILE` and `CA_FILE` for client auth.
See [Configuration → Remote traffic resolver]({{< relref "../configuration" >}}#remote-traffic-resolver-optional).
