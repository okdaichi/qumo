---
title: loadgen
description: Out-of-process capacity load generator — pure remote clients for measuring relay capacity.
weight: 6
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
Usage of loadgen publish:
  -ca string        PEM file of the relay's TLS cert/CA to trust (required)
  -gps float         groups per second (trickle rate) (default 0.5)
  -idle-timeout duration  QUIC max idle timeout (default 30s)
  -keepalive duration     QUIC keep-alive period (default 5s)
  -metrics string    relay /metrics URL (default http://<relay>/metrics)
  -path string        broadcast path (default "/bench/carry")
  -relay string        relay moqt address (host:port) (default "127.0.0.1:4433")
  -size int             frame size in bytes (min 16) (default 64)
  -track string        track name (default "data")
```

## subscribe

```
Usage of loadgen subscribe:
  -ca string        PEM file of the relay's TLS cert/CA to trust (required)
  -hold duration      how long to hold sessions after establishment (default 30s)
  -idle-timeout duration  QUIC max idle timeout (default 30s)
  -keepalive duration     QUIC keep-alive period (default 5s)
  -metrics string    relay /metrics URL (default http://<relay>/metrics)
  -path string        broadcast path (default "/bench/carry")
  -relay string        relay moqt address (host:port) (default "127.0.0.1:4433")
  -results string    dir to append a capacity JSONL record (optional)
  -track string        track name (default "data")
```

## Example

```bash
qumo loadgen publish       --relay <host:4433> --ca <cert.pem>               # trickle source
qumo loadgen subscribe --relay <host:4433> --ca <cert.pem> --hold 15s 12000  # measure N=12000
```

`subscribe --results <dir>` appends a `capacity`-group record to
`results.jsonl`, which the bench dashboard (`scripts/relay_bench_report.ts`)
renders.

## Sweeping / finding the ceiling

Sweeping a list of session counts, or auto-finding the ceiling, is
orchestration and lives in a separate driver — `tools/capacity` — that
composes these primitives (starts a relay + publisher, then probes session
counts):

```bash
go build -o capacity ./tools/capacity

# One box: spawns a local relay (self-signed cert, no openssl), CPU-isolated
# from the load via --relay-cores. A fresh relay starts per probe.
./capacity --start-relay --relay-cores 0-1 --sessions "500 1000 2000" --hold 10s
./capacity --start-relay --relay-cores 0-1 --auto --start 2000 --max 50000 --bisect

# Two hosts: point at a relay running elsewhere; only generates load.
./capacity --relay relay.example.net:4433 --ca cert.pem --auto --start 5000 --max 30000
```

A distributed (multi-machine) run is what a large session-ceiling claim needs
to be *confirmed* rather than extrapolated — see [Observability]({{< relref "../observability" >}})
for the metrics `loadgen` and `capacity` scrape to measure the relay's own
per-session cost.
