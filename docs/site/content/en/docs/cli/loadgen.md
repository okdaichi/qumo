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

`subscribe --results <dir>` appends a machine-readable capacity record to a
JSONL file — useful input for your own sweep/reporting tooling if you're
scripting a series of runs at increasing `N` to find a relay's session
ceiling. See [Observability]({{< relref "../observability" >}}) for the
metrics that back the numbers `loadgen` reports.
