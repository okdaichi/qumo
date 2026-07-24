# Multi-node scalability characterization (Nomad-orchestrated)

**Status:** first empirical multi-node exercise of the qumo relay topology.
**Date:** 2026-07-24. **Environment:** WSL2, 8 cores, 16 GB, single host.
**Orchestration:** HashiCorp Nomad v1.9.5, `raw_exec` driver (native binaries, no
Docker). Relays pinned to cores 0–3; load generators + publishers to cores 4–7.

> **Headline:** the qumo/gomoqt multi-edge fan-out **works** — the project's own
> in-process reference test proves it (0% loss at K = 1, 2, 4). A single-host
> Nomad reproduction hit a **harness/config artifact** (fan-out collapsed to the
> first edge) that is **not** a relay/gomoqt bug and was narrowed but not fully
> isolated. A true distributed capacity multiplier remains issue **#342** (needs
> separate machines).

> **Scope caveat (load-bearing).** Single-host study: hub, all edges, and the load
> generators share one 8-core machine and the loopback interface. Numbers are
> shape-valid, not bare-metal capacity figures.

---

## Topology under test

```
        publisher ──▶ hub (127.0.0.1:4433) ──▶ edge0..3 (4434..4437) ──▶ subscribers
                       (edges dial hub via static PEERS + ANNOUNCE_PLEASE)
```

Each edge is a native `qumo relay` with `PEERS=127.0.0.1:4433`. Harness in
`bench-nomad/` (generates the Nomad job, brings the cluster up, runs a resilient
publisher, drives subscribers). Peer links form (`peers_connected=1`) and
single-edge delivery works end-to-end.

---

## Finding 1 — multi-edge fan-out is CORRECT (a harness artifact, not a bug)

**What was first observed:** with one publisher on the hub and four healthy
edges, only the **first** edge (edge0) received the broadcast; edges 1–3 received
nothing (reproduced 5 ways: direct probe, K-split sweep, late-reconnect, two
publishers/two paths, kill-edge0).

**Why that is NOT a product limitation — the reference test.** The project's own
in-process harness (`relay_chain_scalability_test.go`,
`BenchmarkRelayChain_FanoutSweep`) builds an origin + K leaves via the *same*
`Peers` API and measures per-leaf delivery. Result:

| K leaves | loss% (0 ⇒ every leaf receives fully) | fps |
|---|---|---|
| 1 | **0.00** | 390 |
| 2 | **0.00** | 393 |
| 4 | **0.00** | 381 |

0% loss at K=4 mathematically requires **all four** leaves to receive (one-leaf-only
would be ~75% loss). So gomoqt/qumo fan-out to N edges works when used the
project's way. **Classification: Benchmark harness — not Relay, not gomoqt.**

**What was ruled out in the single-host Nomad reproduction** (each changed in
isolation; edge0-only persisted through all of them):

| Hypothesis | Test | Result |
|---|---|---|
| Node role (`--role edge` + `discoverPeers`) | edges made flat (no role) | still edge0-only |
| LocalResolver polling | `LOCAL_RESOLVER_INTERVAL=0s` (disabled) | still edge0-only |
| mTLS shared client-cert identity collision | distinct per-node certs under a shared CA | still edge0-only |
| QUIC stream limits (~100 default vs 1<<20) | rebuilt relay with `MaxIncoming*Streams=1<<20` | still edge0-only |
| Route-election (primary/alternate) | hub metrics: `routes_retained/replacements/promotions = 0` | not involved |
| Connection dedup by IP | hub `sessions_active` counts all 4 edge sessions | not deduped |

**Residual (unisolated):** a "first-edge-wins" signature remains after all the
above. Remaining differences between the production relay path (`cmd.go run()`)
and the in-process `spinRelay` are the WebTransport server on the listener,
`Allow0RTT`, `EnableStreamResetPartialDelivery`, the 10s/60s keepalive/idle vs
5s/30s, and the separate-process startup ordering. One of these (or a startup
race in the simultaneous-connect path) is the likely cause. **This is a
single-host-harness defect to isolate, not a shipped-relay defect.** The practical
consequence: **this single-host Nomad harness is not (yet) a valid instrument for
measuring multi-edge fan-out** — that verdict must come from the in-process test
(passes) or a proper multi-host deployment (#342).

---

## Finding 2 — per-host ceiling is load-side, not the relay

For the one edge that served, the single-host ceiling reproduces the single-node
conclusion (`CAPACITY-REPORT.md`): the relay is never the bottleneck.

| offered N | relay CPU (of 4 pinned cores) | load-gen CPU (of 4 pinned cores) | held |
|---|---|---|---|
| 2000 | 2.4 | **3.98 (saturated)** | 1931 (96.5%) |
| 4000 | 2.49 | **3.99 (saturated)** | 3317 (82.9%) |

The load generator pins its 4 cores (QUIC handshake crypto) while the relay sits
at ~1.4–2.5 of its 4. On one host the relay budget is fixed at 4 cores regardless
of edge count, so **splitting into K edges cannot multiply per-host capacity** — it
partitions a fixed budget. True horizontal scaling requires relays *and* load on
separate machines. **Classification: Benchmark harness / Hardware (co-located load
generator; single host).**

---

## Bottleneck classification summary

| Observation | Classified as | Evidence |
|---|---|---|
| edge0-only fan-out in the Nomad harness | **Benchmark harness** (single-host config artifact) | in-process reference passes 0% loss K=1,2,4; role/resolver/cert/streams ruled out |
| ~3.3K subs / edge on this host | **Harness / Hardware** (load gen saturates 4 cores) | relay ≤2.5/4 cores at the ceiling |
| No distributed multiplier measurable | **Environment** (single host) | shared cores/NIC/loopback; see #342 |

**No ceiling was attributable to the Relay or gomoqt.**

---

## Next experiments

1. **Isolate the single-host fan-out artifact.** Bisect the residual differences
   (WebTransport listener, `Allow0RTT`, timeouts) and test a strictly-sequential
   edge bring-up to confirm/refute a simultaneous-connect startup race.
2. **Proper multi-host deployment (#342).** Distinct hosts + per-node certs signed
   by a shared CA (the qumo-deploy PKI model) to measure the real N-edge
   multiplier and distributed ceiling — the one environment that can give a true
   multi-node capacity number.

## Reproduction

Scripts under `bench-nomad/` (untracked): `nomad-start.sh`, `gen-cluster.py N`
(`FLAT=1`/`CATLS=1` variants), `run2.sh` (cluster + publisher + sweep), diagnostics
`diag2.sh` / `two-path-fast.sh` / `kill-edge0.sh`, and the reference test wrapper
`run-inproc2.sh` (in-process `BenchmarkRelayChain_FanoutSweep`).
