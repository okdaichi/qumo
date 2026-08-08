---
title: Configuration
description: Environment variables and flags for configuring the qumo relay.
weight: 2
---

Apart from `--role`, qumo is configured entirely through **environment
variables** — there is no config file. This page groups every variable by
concern.

Set them however your platform prefers — inline, an env file, systemd's
`EnvironmentFile=`, or Docker's `--env-file`:

```bash
RELAY_NAME=relay-tokyo qumo relay          # inline
set -a && . ./relay.env && set +a          # from a file you wrote
qumo relay
```

## Server

| Variable | Default | Description |
|---|---|---|
| `RELAY_ADDR` | `:4433` | Bind address (QUIC/MoQT). Dual-stack — binds both IPv4 and IPv6, so `localhost` works on hosts where it resolves to `::1` (e.g. Windows). Also serves HTTP health/metrics on the same port. |
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS certificate and key. Required — the relay exits at startup if it can't load them. |

The node's **topology role** is a CLI flag, not an env var:

```bash
qumo relay --role hub    # or "edge"; omit for a standalone / flat relay
```

## Node identity

| Variable | Default | Description |
|---|---|---|
| `RELAY_NAME` | hostname | Human-readable node identifier. |
| `ADVERTISE_ADDR` | (empty) | Public address advertised to peers. Required when `RELAY_ADDR` is a wildcard (`0.0.0.0` / `::`). |

## Static peers

| Variable | Default | Description |
|---|---|---|
| `PEERS` | (empty) | Comma-separated peer relay addresses (`moqt://host:4433,...`). The node connects to each and relays their announcements. |

See [Deployment → Peer topology]({{< relref "deployment/peer-topology" >}}) for
how static peers, Nomad discovery, and the remote resolver fit together.

## Local resolver — Nomad-native discovery

| Variable | Default | Description |
|---|---|---|
| `LOCAL_RESOLVER_ADDR` | `http://localhost:4646` | Nomad HTTP API address (set automatically when running inside Nomad). |
| `LOCAL_RESOLVER_SERVICE_NAME` | `qumo-relay` | Nomad service name to query for peer discovery. |
| `LOCAL_RESOLVER_INTERVAL` | `15s` | Polling interval. |

See [Deployment → Nomad]({{< relref "deployment/nomad" >}}).

## Remote traffic resolver (optional)

| Variable | Default | Description |
|---|---|---|
| `REMOTE_RESOLVER_URL` | (empty) | Base URL of the remote traffic resolver (e.g. qumo backend control plane). Enables cross-cluster hub discovery. |
| `REMOTE_AUTH_TOKEN` | (empty) | Bearer token sent to the remote resolver. |
| `REMOTE_RESOLVE_INTERVAL` | `15s` | Polling interval. |
| `REMOTE_TLS_ENABLED` | `false` | Enable TLS (mTLS, using `CERT_FILE`/`CA_FILE`) for the remote resolver connection. |

## mTLS (optional)

| Variable | Default | Description |
|---|---|---|
| `CA_FILE` | (empty) | PEM CA certificate. When set, mutual TLS is enabled between peers. |
| `MTLS_REQUIRED` | `false` | When `true`, every connection must present a client cert signed by `CA_FILE`. |

See [Deployment → TLS & mTLS]({{< relref "deployment/tls" >}}).

## Graceful migration / GOAWAY (optional)

| Variable | Default | Description |
|---|---|---|
| `GOAWAY_REDIRECT_URI` | (empty) | Escape-hatch mobility primitive: on shutdown, redirect clients/peers to a successor relay. Route/subscription migration (make-before-break) is the primary mechanism — set this only when you also want graceful-shutdown redirects. |

## Capacity

| Variable | Default | Description |
|---|---|---|
| `GROUP_CACHE_SIZE` | `8` | Completed groups each track's ring retains for late/backfill subscribers. Raise to absorb longer subscriber startup lag, at the cost of per-track memory. |
| `FRAME_CAPACITY` | `1500` | Frame buffer capacity in bytes (roughly one network MTU). |
| `RELAY_UDP_RCVBUF` | `262144` | UDP receive buffer (`SO_RCVBUF`) for the relay's QUIC listener socket. Raise on deployments pushing beyond ~15K concurrent sessions in burst. Set to `0` to use the unmodified OS default. |
| `RELAY_GOGC` | (unset) | GC target percentage. Opt-in: if unset (and `GOGC` is unset), the Go runtime default (100) is used. A fan-out relay's goroutine stacks dominate RSS, so GC-scan CPU grows with session count; `RELAY_GOGC=800..1600` reached ~18–20K sessions on an 8-core host. `GOGC` (the runtime's own env var) always wins if set. Do **not** use `GOMEMLIMIT` for this workload — it forces constant GC and collapses throughput. |

Run `qumo doctor` to see the effective GC target, which input won, and why —
read-only, it changes nothing. See [Observability]({{< relref "observability" >}}).

## Profiling

| Variable | Default | Description |
|---|---|---|
| `RELAY_PPROF` | `0` | Opt-in `net/http/pprof` endpoints (`/debug/pprof/...`) alongside `/metrics`. Off by default — enable only on a trusted/loopback interface. |

## CORS — WebTransport origin check

| Variable | Default | Description |
|---|---|---|
| `CORS_ALLOWED_ORIGINS` | (unset) | Origins permitted to open WebTransport sessions to `qumo relay`, `qumo rtmp`, and `qumo rtsp`/`rtsp-push`. Comma-separated. `*` allows any origin; `same-host` allows any port on the request's own host. If unset, only same-origin and headerless (non-browser) clients are accepted. |

## Credential auth & metering (optional)

| Variable | Default | Description |
|---|---|---|
| `QUMO_CREDENTIAL_URL` | (unset) | Base URL of the qumo credential server. When set, the relay authenticates publisher JWTs via `POST /v1/credentials/introspect` and reports cumulative ingress/egress byte totals via `POST /v1/usage/events`. Leave unset for open-relay mode. |
| `QUMO_RELAY_TOKEN` | (unset) | Shared bearer token the relay presents to the credential server. Must match the server's configured token. |
