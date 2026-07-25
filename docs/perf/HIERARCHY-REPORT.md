# Hierarchical relay scaling on a single host

**Objective:** characterize how a hierarchical qumo topology (one hub fanning out
to K edge relays, subscribers spread evenly across the edges) behaves on **one
physical machine** as K grows — before any multi-host deployment.

**Setup.** WSL2, 8 cores, 16 GB. HashiCorp Nomad `raw_exec` (native binaries, no
Docker). **All relays pinned to cores 0–3; load generators + publisher pinned to
cores 4–7** (consistent across every topology). One publisher to the hub, constant
**30 fps × 1200 B**. Subscribers launched **concurrently** and distributed evenly
across the serving relays (the hub itself for K=0). Per level the driver ramps
total subscribers and records, for every relay, CPU / RSS / goroutines / GC /
allocations / egress, plus end-to-end p50/p95/p99 latency and frame loss. Harness:
`bench-nomad/` (`run-study.sh`, `study.py`, `gen-cluster.py`).

> **One load-bearing caveat.** This is a single host: the load generators and all
> relays share the same 8 cores and loopback. The relay is ~5–10× more
> CPU-efficient than the load generator (which pays full QUIC-handshake crypto per
> session), so **the maximum-subscriber ceiling here is load-generator-bound, not
> relay-bound.** Absolute latency also includes the co-located generator's read
> scheduling. The topology *comparisons* (all at identical load + pinning) remain
> valid; the absolute capacity numbers are single-host, not bare-metal.

---

## Headline results

| topology | relays | max sustained subs | agg Mbps @ max | p99 @ N=1000 | **hub CPU @ N=1000** | edge CPU sum @ N=1000 | total relay CPU @ N=1000 |
|---|---|---|---|---|---|---|---|
| 1 relay (direct) | 1 | **992** | 285 | 227 ms | **1.881 cores** | 0 | 1.88 |
| hub + 1 edge | 2 | 1431 | 410 | 215 ms | **0.029** | 1.73 | 1.76 |
| hub + 2 edges | 3 | 1000* | 288 | **88 ms** | 0.037 | 1.86 | 1.90 |
| hub + 4 edges | 5 | 1500 | 433 | **45 ms** | 0.039 | 1.79 | 1.83 |
| hub + 8 edges | 9 | 1492 | 430 | **34 ms** | 0.047 | 1.73 | 1.78 |

\* K=2 "max sustained" is capped by the 300 ms p99 gate (its N=1500 point had only
0.38 % loss but p99 456 ms); by loss alone it also holds ~1500.

**Three clear answers:**

1. **Does hierarchical fan-out reduce hub CPU? — Yes, dramatically.** Serving 1000
   subscribers, a lone hub burns **1.88 cores**; with even one edge the hub drops
   to **0.03 cores (≈ 40–65× less)** — it becomes a cheap fan-in→fan-out
   multiplexer feeding K edges, and all egress work moves to the edges.

2. **Does latency improve? — Yes, monotonically with K, at fixed load.** p99 at
   N=1000 falls **227 → 215 → 88 → 45 → 34 ms** as K goes 0→1→2→4→8 (p50 11.3 → 8.2
   ms). Fewer subscribers per edge ⇒ less per-connection egress queueing ⇒ lower
   tail. This is the genuine relay-side benefit of fan-out.

3. **Does max capacity scale with relay count? — No. It is flat/sublinear.** Max
   sustained subscribers stays ~**1000–1500** from 1 relay to 9. Going from 1 to 9
   relays multiplies capacity by ~1.5×, not 9×. **Adding relays past ~2–4 edges
   buys no capacity** — only latency and hub-offload.

---

## Full ramp data

```
 K relays     N  conn  loss%  p50ms  p99ms   Mbps hubCPU edgeCPU totCPU sust
 0      1   500   500   0.00    4.6     31    144  1.001   0.000   1.00 Y
 0      1  1000   992   0.22   11.3    227    285  1.881   0.000   1.88 Y
 0      1  1500  1494   7.31   21.4    737    399  2.469   0.000   2.47 n   <- hub saturates
 0      1  2000  1978  16.08   42.0   1000    478  2.750   0.000   2.75 n
 1      2  1000  1000   0.00   11.8    215    289  0.029   1.731   1.76 Y
 1      2  1500  1431   0.62   17.8    111    410  0.029   2.559   2.59 Y
 1      2  2000  1893   0.54   43.6    538    542  0.029   2.184   2.21 n
 2      3  1000  1000   0.00   10.1     88    288  0.037   1.860   1.90 Y
 2      3  1500  1500   0.38   19.2    456    430  0.036   2.676   2.71 n
 4      5  1000  1000   0.00    8.9     45    288  0.039   1.792   1.83 Y
 4      5  1500  1500   0.00   14.5    232    433  0.039   2.453   2.49 Y
 4      5  2000  1806   0.00   16.4    877    522  0.041   2.177   2.22 n
 8      9  1000  1000   0.00    8.2     34    288  0.047   1.733   1.78 Y
 8      9  1500  1492   0.05   13.2    125    430  0.051   1.289   1.34 Y
 8      9  2000  2000   1.59   24.8    340    567  0.047   2.964   3.01 n
 8      9  3000  2998  35.69  174.8   1000    555  0.048   1.748   1.80 n
 8      9  4000   148 100.00      -      -      0  0.056   0.553   0.61 n   <- establishment collapse
```

## Work distribution (cross-section at N=1000)

| K | per-edge subs | total allocs/s | total egress Mbps | total RSS MB | hub allocs/s |
|---|---|---|---|---|---|
| 0 | 1000 (hub) | 656 K | 271 | 981 | 656 K (hub does all) |
| 1 | 1000 | 643 K | 246 | 793 | 2.7 K (idle) |
| 2 | 500 | 667 K | 257 | 884 | 3.1 K |
| 4 | 250 | 636 K | 240 | 811 | 4.6 K |
| 8 | 125 | 636 K | 240 | 944 | 7.2 K |

**Total work (allocations, egress bytes, memory) is topology-invariant and splits
evenly — each edge does 1/K of it.** Hierarchy is a *load balancer*, not a capacity
multiplier: it moves work off the hub and spreads it, but the machine does the same
total work. GC stayed small throughout (≤ ~12 ms max pause, ~0.1 GC/s, GOGC=800) —
never the limiter.

## Scaling efficiency (vs ideal linear)

efficiency = max-subs(K) / (max-subs(1 relay) × relay-count):

| topology | relays | max subs | efficiency vs linear |
|---|---|---|---|
| 1 relay | 1 | 992 | 100 % (baseline) |
| hub+1 | 2 | 1431 | 72 % |
| hub+2 | 3 | 1000 | 34 % |
| hub+4 | 5 | 1500 | 30 % |
| hub+8 | 9 | 1492 | **17 %** |

Efficiency collapses toward `1/relays` — the signature of **flat scaling**.

---

## Saturation & bottleneck classification

| topology | first saturated component | first saturated relay | limiter | scaling |
|---|---|---|---|---|
| 1 relay | **hub egress CPU** (→2.75 cores) | the hub | **hub** | — |
| hub+K (K≥1) | edge egress + co-located **load generator** | the edges (collectively) | **edges / load side** | flat |

- **K=0:** the single hub is the bottleneck — its egress CPU climbs to ~2.75 cores
  and loss explodes past N=1500. Classic single-relay wall.
- **K≥1:** the hub is *never* the bottleneck (0.03–0.05 cores). The binding
  constraints become (a) **edge egress** (edges share cores 0–3; collectively
  ≤ ~3.3 of 4) and (b) the **co-located load generator** (cores 4–7) for
  establishment + frame reads. The relay side never fully saturates its 4 cores at
  the max point, so the true ceiling is the load generator — the K=8, N=4000
  collapse (connected 148/4000) is pure establishment failure, load-side.

---

## Why scaling stops improving (evidence, not speculation)

1. **Fixed core budget.** Every relay is pinned to cores 0–3. Adding relay
   processes subdivides a fixed 4-core budget — it creates no new compute. Measured:
   total relay CPU at N=1000 is **~1.8 cores for every K** (1.88, 1.76, 1.90, 1.83,
   1.78). Same machine, same total compute.

2. **Total work is invariant.** allocs/s (~640 K), egress (~250 Mbps), and RSS
   (~900 MB) are constant across K and split evenly (total/K per edge). Fan-out
   redistributes; it does not reduce. So capacity can’t rise on one host.

3. **The ceiling is the co-located load generator, not the relay.** At every
   topology’s max point the relay side sits below full saturation (≤3.4/4 cores)
   while loss / establishment failures appear first. The load generator (QUIC
   handshake crypto + per-frame reads on 4 cores) binds first — the known
   single-host confound (relay ≈5–10× more CPU-efficient). This is why max-subs is
   flat regardless of topology.

4. **Latency improvement is real and relay-side.** p99 falls monotonically as
   subscribers-per-edge falls (1000 → 125): each edge holds fewer concurrent QUIC
   egress streams, so per-connection head-of-line queueing drops. Measured p99
   227 ms → 34 ms with total CPU held constant. Fan-out parallelizes egress across
   more sockets/goroutine sets — that is the genuine win.

5. **The extra hub→edge hop is nearly free in CPU** (hub 0.03 cores feeding K
   edges) and negligible in latency on loopback, so it does not offset the
   egress-queueing reduction — net latency improves.

6. **GC / scheduler are not the limiter** — pauses ≤ ~12 ms, ~0.1 GC/s across all
   topologies.

7. **K=8 is the practical single-host limit.** Nine relay processes needed a
   lowered Nomad memory reservation (2 GB/relay overflowed 16 GB — fixed to 400 MB),
   and eight concurrent load processes on four cores collapse establishment past
   ~2000 subscribers. Past ~4 edges: shrinking latency gains, rising fragility, zero
   capacity gain.

---

## Conclusion

On a **single host**, hierarchical fan-out is a **latency-and-offload optimization,
not a capacity multiplier**:

- **Hub CPU:** cut ~40–65× (1.88 → 0.03 cores) — work fully offloaded to edges.
- **Latency:** p99 improved ~7× (227 → 34 ms) via per-edge load reduction.
- **Capacity:** **flat** — ~1000–1500 sustained subscribers from 1 to 9 relays;
  efficiency vs linear falls 72 % → 17 %. The knee is **~2–4 edges**; beyond it,
  no capacity benefit and growing fragility.
- **Bottleneck:** the hub for K=0; the edges + co-located load generator (shared
  cores) for K≥1 — never a relay-code or gomoqt limit.

To turn the hub-offload into *real* capacity gains, each edge needs its own cores
and NIC — i.e. **more hosts**. That is the multi-host study (issue #342): the
single-host result here says hierarchy is worth deploying for **hub relief and
tail-latency**, and predicts near-linear capacity **only** once edges stop sharing
a machine.

*Plots: see the companion `study-report.html` (relay-count vs max subs, throughput,
p99, hub CPU, edge CPU, scaling efficiency).*
