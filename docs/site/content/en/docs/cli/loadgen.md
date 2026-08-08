---
title: loadgen
description: Drive an out-of-process capacity load against a running relay.
weight: 7
---

Out-of-process capacity primitives against a running qumo relay. Both
subcommands are pure remote clients — they dial `--relay` (trusting `--ca`)
and never spawn a relay themselves. This matters: running load clients and
the relay in one process makes client-side QUIC-handshake CPU, not the relay,
the bottleneck.

```
Usage: qumo loadgen <subcommand> [flags]

Subcommands:
  publish          Publish one trickle track to the relay (keep running during a run)
  subscribe <N>    Launch N subscriber sessions and measure the relay's hold + per-session cost
```

## publish

```
Usage: qumo loadgen publish [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--ca <file>` | (required) | PEM file of the relay's TLS cert/CA to trust. |
| `--relay <host:port>` | `127.0.0.1:4433` | Relay MoQT address to dial. |
| `--path <path>` | `/bench/carry` | Broadcast path. |
| `--track <name>` | `data` | Track name. |
| `--gps <float>` | `0.5` | Groups per second (trickle rate). |
| `--size <bytes>` | `64` | Frame size in bytes (min 16). |
| `--metrics <url>` | `http://<relay>/metrics` | Relay `/metrics` URL. |
| `--keepalive <dur>` | `5s` | QUIC keep-alive period. |
| `--idle-timeout <dur>` | `30s` | QUIC max idle timeout. |

## subscribe

```
Usage: qumo loadgen subscribe [flags] <N>
```

| Flag | Default | Description |
|---|---|---|
| `--ca <file>` | (required) | PEM file of the relay's TLS cert/CA to trust. |
| `--relay <host:port>` | `127.0.0.1:4433` | Relay MoQT address to dial. |
| `--path <path>` | `/bench/carry` | Broadcast path. |
| `--track <name>` | `data` | Track name. |
| `--hold <dur>` | `30s` | How long to hold sessions after establishment. |
| `--results <dir>` | (optional) | Directory to append a capacity JSONL record to. |
| `--metrics <url>` | `http://<relay>/metrics` | Relay `/metrics` URL. |
| `--keepalive <dur>` | `5s` | QUIC keep-alive period. |
| `--idle-timeout <dur>` | `30s` | QUIC max idle timeout. |

## Example

```bash
qumo loadgen publish   --relay <host:4433> --ca <cert.pem>                    # trickle source
qumo loadgen subscribe --relay <host:4433> --ca <cert.pem> --hold 15s 12000   # measure N=12000
```

`subscribe --results <dir>` appends a machine-readable capacity record to a
JSONL file — useful input for your own sweep/reporting tooling if you're
scripting a series of runs at increasing `N` to find a relay's session
ceiling. See [Observability]({{< relref "../observability" >}}) for the
metrics that back the numbers `loadgen` reports.
