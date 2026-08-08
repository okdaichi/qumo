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

- incoming connections must present a client cert signed by this CA;
- the dialer presents this node's `CERT_FILE` cert to remote relays and trusts only the CA pool;
- remote resolver clients also present the client cert and verify the remote server against this CA (when `REMOTE_TLS_ENABLED=true`).

Because mTLS is required by default once `CA_FILE` is set, browsers — which
don't present a client cert — can no longer connect. If the same relay also
serves browser/WebTransport traffic directly, set `MTLS_REQUIRED=false`: peer
certs are still verified when presented, but connections without one are
accepted.

| Variable | Default | Description |
|---|---|---|
| `CA_FILE` | (unset) | PEM CA certificate. Leave unset to disable mTLS entirely. |
| `MTLS_REQUIRED` | `true` | Whether every connection must present a client cert signed by `CA_FILE`. Set `false` to also accept connections without one. |

## Remote resolver TLS

`REMOTE_TLS_ENABLED=true` enables mTLS specifically for the connection to a
remote traffic resolver, reusing `CERT_FILE` and `CA_FILE` for client auth.
See [Configuration → Remote traffic resolver]({{< relref "../configuration" >}}#remote-traffic-resolver-optional).
