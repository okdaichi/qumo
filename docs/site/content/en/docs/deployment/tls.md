---
title: TLS & mTLS
description: Certificates for the relay, and mutual TLS between peer relays.
weight: 4
---

## Server TLS

qumo requires TLS 1.3 for its QUIC listener, configured via `CERT_FILE` /
`KEY_FILE` (see [Configuration → Server]({{< relref "../configuration" >}}#server)
for the full variable reference).

For local development, generate a browser-trusted cert with
[mkcert](https://github.com/FiloSottile/mkcert):

```bash
mkcert -install
mkcert -cert-file certs/server.crt -key-file certs/server.key localhost 127.0.0.1 ::1
qumo relay
```

(`qumo playground` needs no manual cert — it generates and trusts its own dev
certificate automatically.)

## Mutual TLS between peers (optional)

Setting `CA_FILE` enables mutual TLS for the relay's peer mesh:

- incoming peer connections that present a client cert are verified against this CA;
- the dialer presents this node's `CERT_FILE` cert to remote relays and trusts only the CA pool;
- remote resolver clients also present the client cert and verify the remote server against this CA (when `REMOTE_TLS_ENABLED=true`).

Connections **without** a client cert are still allowed by default, so
browser/WebTransport clients keep working unmodified — set `MTLS_REQUIRED=true`
for relay-only clusters with no direct browser traffic. See
[Configuration → mTLS]({{< relref "../configuration" >}}#mtls-optional) for
the `CA_FILE` / `MTLS_REQUIRED` variable reference.

## Remote resolver TLS

`REMOTE_TLS_ENABLED=true` enables mTLS specifically for the connection to a
remote traffic resolver, reusing `CERT_FILE` and `CA_FILE` for client auth.
See [Configuration → Remote traffic resolver]({{< relref "../configuration" >}}#remote-traffic-resolver-optional).
