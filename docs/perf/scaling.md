# Scaling

How qumo's active fan-out capacity responds to three levers: more subscribers,
more cores, and more relay nodes (hierarchy). The headline is that only one of
these — topology, and only across separate hosts — meaningfully raises capacity.
Cores plateau quickly, and a single host cannot show real horizontal scaling.

## Subscriber scaling

Linear to ~1000 subscribers under the audio baseline, then a sharp knee. The
detail and the curve are in [baseline](baseline.md). This page covers the
*levers* applied to that curve.

## Core scaling

A GOMAXPROCS sweep of the fan-out bench (audio baseline, 8 s runs, in-process so
the publisher + relay + subscribers scale together):

| GOMAXPROCS | N=1000 loss % | N=1000 p99 | N=1500 loss % | N=1500 p99 |
|---|---|---|---|---|
| 2 | 41.7 | 180 ms | 59.4 | 328 ms |
| **4** | **1.2** | 84 ms | 21.0 | 122 ms |
| **8** | 2.3 | 68 ms | **9.3** | 136 ms |
| 16 | 2.9 | 94 ms | 12.9 | 150 ms |

**Capacity does not scale with cores — it plateaus at ~4–8.**

- Going 2 → 4 cores is transformative: at 1000 subscribers loss falls from 42 %
  to 1.2 % and fps jumps 17 → 28.5. Below ~4 cores the per-frame fan-out herd
  starves the run queue and the workload collapses.
- Going 4 → 8 → 16 cores gives **no further capacity** and marginally regresses
  (loss creeps 1.2 → 2.3 → 2.9 %; p99 drifts up). Extra cores add scheduler
  overhead without relieving the per-frame fan-out drain.

> **Note:** Hardware is therefore **not** the lever past ~8 cores. This is
> consistent with the isolated relay never saturating 4 cores and with the
> socket-PPS ceiling being core-independent. In-process GOMAXPROCS scales
> publisher + relay + subscribers together, so treat the plateau as the signal,
> not the exact per-point numbers.

## Topology scaling (hierarchy)

A hierarchical topology fans one hub out to K edge relays, with subscribers
spread evenly across the edges. This divides each edge's fan-out by K.

### Latency: hierarchy works

p99 at 1000 subscribers falls monotonically as the fan-out is divided:

| edges (K) | subs/edge | p99 @ N=1000 | hub CPU |
|---|---|---|---|
| 0 (flat) | 1000 | **227 ms** | 1.88 |
| 2 | 500 | 88 ms | — |
| 4 | 250 | 45 ms | — |
| 8 | 125 | **34 ms** | 0.05 |

That is a ~7× p99 reduction (227 → 34 ms) by cutting subscribers-per-relay from
1000 to 125. The hub offloads from 1.88 cores to ~0.05 — it becomes a cheap
fan-in → fan-out multiplexer. The mechanism is exactly the fan-out drain model:
dividing the subscriber count divides R.wake proportionally.

### Capacity: does not multiply on one host

Maximum sustained subscribers (p99 < 300 ms, loss < 1 %, ≥ 0.95·N connected):

| relays | edges (K) | max sustained subs | efficiency vs linear |
|---|---|---|---|
| 1 | 0 | ~1000 | 100 % (baseline) |
| 3 | 2 | ~1000 | 34 % |
| 5 | 4 | ~1500 | 30 % |
| 9 | 8 | ~1500 | **17 %** |

Efficiency collapses toward `1/relays` — the signature of flat scaling. The
reason is structural: on one host every relay is pinned to the same cores 0–3,
so adding relay processes **subdivides a fixed 4-core budget** rather than adding
compute. Total relay CPU at 1000 subscribers is ~1.8 cores for *every* K
(1.88 / 1.90 / 1.83 / 1.78). Total work — allocations, egress bytes, memory — is
topology-invariant and splits evenly across edges.

At the K=8 ceiling the relays use only 1.78 of 4 cores (56 % idle). The binding
constraint above ~1500–2000 subscribers is the **co-located load generator**
(handshake crypto + frame reads on cores 4–7), not the relays.

> **Warning:** Do not claim K× production capacity from these single-host
> numbers. Hierarchy on one host is a **latency and hub-offload optimization,
> not a capacity multiplier**. The practical knee is ~2–4 edges; beyond it the
> latency gains diminish and operational fragility rises.

### Per-node resources (K=8, N=1000)

| node | CPU | RSS | goroutines | sessions |
|---|---|---|---|---|
| hub | 0.047 c | 41 MB | 121 | 9 (the edges) |
| each edge (×8) | ~0.22 c | ~118 MB | 61 | 126 |

## What would show real horizontal scaling

True K× capacity requires **each edge on its own host** — its own cores, NIC, and
recv buffer. On a single host the experiment is structurally unable to measure
it: the load generator caps out at ~1500–2000 media subscribers, and all relays
share one core budget.

The unblocked experiment is distributed load generation (qumo issue #342):
publisher, N subscriber generators, and K edges each on separate hosts with a
tuned kernel (`net.core.rmem_max` ≥ 7 MB). Ramp per-edge subscribers until the
*relay's* p99 crosses the SLO. That isolates the true per-edge capacity and
turns the hierarchy latency win into a real capacity number. Until that runs,
the defensible statement is:

> A flat relay serves ~1000 subscribers within the 300 ms SLO. Hierarchy
> collapses p99 ~7× at equal load; projected to scale ~K× across separate hosts,
> **unmeasured**.

## See also

- [Baseline](baseline.md) — the subscriber curve these levers act on.
- [Bottleneck attribution](bottleneck-attribution.md) — why cores plateau and
  why the fan-out drain is the ceiling.
