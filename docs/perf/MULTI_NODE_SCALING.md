# qumo — horizontal (multi-node) scaling of active fan-out capacity

**Question:** the single relay knees at ~1000 active audio subscribers because
`groups/sec × subscribers` creates per-frame broadcast/scheduler pressure. Does
adding relay nodes (hub → K edges) scale active fan-out capacity?

**Answer up front:**
- **Latency: YES, decisively.** At a fixed 1000 subscribers, hub→8 edges cuts p99
  from **227 ms to 34 ms (~7×)** by dividing subscribers-per-relay. The scaling
  model (`fan-out cost ∝ subscribers/relay`) is **confirmed**. **[FACT]**
- **Capacity on this hardware: NO.** On a single host all relays share one fixed
  4-core budget, so adding relays *subdivides* compute rather than adding it. Max
  sustainable subscribers stays **flat (~1000–1500)** across K=1…8; scaling
  efficiency collapses **100 % → 19 %**. This is an **environment limitation, not a
  relay property.** **[FACT]**
- **Capacity on real separate hosts: UNKNOWN, projected ~linear.** Each edge's cost
  equals a single relay serving `N/K` subscribers, so with per-edge hardware the
  projection is ~K× capacity — but this **cannot be measured on one host** and
  requires distributed deployment (#342). **[INFERENCE / UNKNOWN]**

Labels: **[FACT]** directly measured · **[INFERENCE]** supported by data ·
**[UNKNOWN]** needs real distributed deployment.

---

## 1. Existing infrastructure (reused, nothing duplicated)

| Asset | Role here | Reuse |
|---|---|---|
| **`bench-nomad/`** (`run-study.sh`, `study.py`, `gen-cluster.py`) | Nomad `raw_exec` cluster: hub + K edges, relays pinned cores 0–3, loadgen 4–7, per-relay `/metrics` scrape | **This IS the multi-node experiment.** Ran as-is |
| `study-all.jsonl` | K=1,2,4,8 × N=500–4000, audio workload, per-relay CPU/RSS/GC/allocs + e2e latency/loss | Primary dataset |
| `confirm-slo.jsonl` | K=0,4,8 × N=1000–2000 on **current code** (post-#348) | Current-code confirmation |
| **`FanoutSingleRelay`** bench | single relay → per-edge cost model (an edge serving `N/K` = `FanoutSingleRelay(N/K)`) | Per-edge projection |
| stage instrument (`-tags instrument`) + `RELAY_PPROF` | e2e latency, R.fill/R.wake split, per-relay pprof | Mechanism + profiling |
| `docs/perf/{HIERARCHY-REPORT,SLO-CAPACITY-STUDY,FANOUT-MECHANISM,AUDIO-BASELINE-MODEL}.md` | Prior analyses of this exact topology | Cross-referenced |

**No new benchmark was created.** The Nomad harness already runs the requested
hub→edge topologies with the identical audio workload; the single-relay bench
supplies the per-edge cost model.

## 2. Experiment & the environment limitation (read this first)

Topology: 1 hub relay → K edge relays → subscribers even across edges; K = 0
(single), 1, 2, 4, 8. Publisher → hub. Subscribers subscribe to edges.

**[FACT] Hard limitation — no separate hosts.** Everything runs on **one WSL2 host,
8 cores**: *all* relays (hub + K edges) are pinned to cores **0–3**, the load
generator + publisher to **4–7**. Adding edges therefore **subdivides a fixed
4-core relay budget** — it adds processes, not compute or NICs. Consequently this
environment **cannot** test whether relays on *separate hosts* multiply capacity;
it can only test latency redistribution and the single-host ceiling.
**Any "K× capacity" projection here is inference, not measurement (§5, #342).**

## 3. Workload (identical across all topologies)

Publisher: **30 groups/s, 1 frame/group, 1200 B, paced** (`time.Sleep` between
groups; measured inter-arrival p50 33.7 ms — no burst, see
`PUBLISHER-CADENCE-CHECK.md`). Sustained = p99 < 300 ms **and** loss < 1 % **and**
connected ≥ 95 % of target. Frame size 1200 B is heavier than real audio (~160 B),
so capacity numbers are conservative. **[FACT]**

## 4. Results per topology

### 4a. Capacity — max sustainable subscribers (single host)

| Relays | edges (K) | max sustainable subs | subs/relay | ideal (K × single) | **efficiency** |
|---|---|---|---|---|---|
| 1 | 0 | **1000** | 1000 | 1000 | 100 % |
| 1+hub | 1 | 1500 | 1500 | 1000 | (150 %)\* |
| 2+hub | 2 | 1000 | 500 | 2000 | 50 % |
| 4+hub | 4 | 1500 | 375 | 4000 | 38 % |
| 8+hub | 8 | 1500 | 187 | 8000 | **19 %** |

\* K=1 is a pure forwarder (hub off-loaded to one edge) and shows harness variance;
the trend from K=2→8 is the signal. **[FACT] Capacity is flat (~1000–1500),
efficiency collapses toward 1/relays** — the fixed-core signature.

### 4b. Latency relief — fixed total N=1000, vary K

| K | subs/edge | **e2e p99** | e2e p50 | hub CPU | Σ edge CPU |
|---|---|---|---|---|---|
| 0 | 1000 | **227 ms** | 11.3 ms | 1.88 | — |
| 1 | 1000 | 215 ms | 11.8 ms | 0.03 | 1.73 |
| 2 | 500 | 88 ms | 10.1 ms | 0.04 | 1.86 |
| 4 | 250 | 45 ms | 8.9 ms | 0.04 | 1.79 |
| 8 | 125 | **34 ms** | 8.25 ms | 0.05 | 1.73 |

**[FACT] p99 falls 227 → 34 ms as subscribers/edge falls 1000 → 125.** Hub CPU
drops 1.88 → 0.05 cores (pure fan-in multiplexer); total relay CPU (~1.8 cores) is
**conserved** across K — hierarchy *redistributes* fan-out work off the hub onto
edges, it doesn't reduce it.

### 4c. Per-edge resource & current-code confirmation

**[FACT]** K=8/N=1000, per edge: **0.22 cores, ~118 MB RSS, 61 goroutines, 126
sessions.** Total relay CPU = **1.78 of 4 pinned cores — the relays are NOT
saturated** (56 % idle) even at the flat ceiling. Current code (confirm-slo,
post-#348) reproduces the latency relief: K=4/N=1000 p99 29 ms, K=8/N=1000 p99
20 ms; and the collapse above the loadgen ceiling (K=4/N=2000 → 84 % loss).

## 5. Scaling-model validation

**[FACT] The model `fan-out cost ∝ subscribers/relay` holds.** Dividing 1000
subscribers across K edges divides subscribers/edge (1000→125) and p99 falls
monotonically (227→34 ms). This is *why* hierarchy helps latency: it shrinks the
per-relay broadcast herd (each per-frame `broadcast()` wakes only the edge's share,
not all N).

**[FACT] But capacity does not multiply on one host.** Ideal linear scaling (1→2→4
relays → 2×→4× users) is **refuted here**: efficiency is 50 %/38 %/19 % at
K=2/4/8. Root cause is measured, not hypothesized — all relays share 4 cores, and
each edge does 1/K of a conserved ~1.8-core total, so K edges can't each run at the
single-relay operating point simultaneously.

**[INFERENCE] On separate hosts, ~linear scaling is plausible.** An edge serving
`N/K` subscribers behaves like `FanoutSingleRelay(N/K)`, which is far below its own
knee (e.g. 125 subs ≈ p99 2 ms). If each edge had its **own** cores/NIC, K edges
could each run at the ~1000-subscriber single-relay operating point → **~K×1000
total**. This is a projection from the single-relay curve + a linear-hardware
assumption. **[UNKNOWN until #342.]**

## 6. The next bottleneck after hierarchy

**[FACT] On one host there is no "after" — the relays never became the
bottleneck.** At the flat ceiling the relays use 1.78/4 cores (headroom), GC is
≤ 12 ms/≤ 0.1 per s, and per-edge goroutines are ~61. The binder is **external to
the relays**: the co-located load generator (cores 4–7) can't establish/sustain
beyond ~1500–2000 subscribers (`loadgen-underconnected`, `fps` collapse), the same
single-host confound documented in `SLO-CAPACITY-STUDY.md`.

**[INFERENCE] The per-relay mechanism, at any scale, stays the broadcast→scheduler
herd.** An edge's cost is the single-relay pattern (`FANOUT-MECHANISM.md`,
`AUDIO-BASELINE-MODEL.md`): R.fill flat ~6 µs, R.wake = the whole latency, ~40 % of
CPU in the Go scheduler waking goroutines, ~30 % quic-go per-connection. Hierarchy
lowers each edge's N, so each edge hits that wall later — it does not remove the
wall.

**[UNKNOWN] The true post-hierarchy bottleneck on real hosts.** Once each edge runs
on its own cores at its ~1000-subscriber knee, the next limit is one of: (a) the
per-edge scheduler/quic-go wall again (same mechanism), (b) the **hub fan-in**
(hub must feed all K edges — trivial at K=8, unknown at K=100s), (c) **NIC/socket
PPS** per edge (~222 K pps pure-QUIC ceiling), or (d) cross-host network. Only a
distributed deployment (#342) can rank these.

## 7. Final answers

1. **How many active audio subscribers can one relay support?**
   **~1000** at p99 ≤ ~56 ms and < 2 % loss (in-process, current code); the
   isolated relay's loss crosses 1 % at ~1000–1500 while using only 1.9–2.5/4
   cores. Conservative (1200 B > real audio 160 B). **[FACT]**

2. **How does that change with 2/4/8 relays?**
   **On this single host: barely** — flat ~1000–1500 total (efficiency 50/38/19 %),
   because there are no extra cores. **On separate hosts: projected ~2×/4×/8×
   (~2000/4000/8000)** — inference from the per-edge cost model, **unmeasured**
   (#342). **[FACT + INFERENCE/UNKNOWN]**

3. **Does hierarchy solve the fan-out *latency* problem?**
   **Yes.** p99 227 → 34 ms at N=1000 (7×) by cutting subscribers/relay; the hub
   off-loads to ~0.05 cores. It solves latency by *dividing* the herd. **[FACT]**

4. **What is the next bottleneck after hierarchy?**
   On one host, **not the relay** (1.78/4 cores; the binder is the co-located
   loadgen). The relay's own wall stays the **broadcast→scheduler herd** per edge.
   The genuine post-hierarchy bottleneck on real hosts is **unknown** — hub fan-in,
   per-edge scheduler/quic-go, or NIC PPS (#342). **[FACT + UNKNOWN]**

5. **What optimization should be investigated next?**
   Hierarchy is a *deployment workaround* for the fan-out herd, not a code fix. The
   direct attack on the root cause is the **per-frame `broadcast()` O(N) wakeup**:
   investigate reducing the simultaneous-wakeup cost (e.g. sharded/batched egress
   wakeup, or waking only subscribers actually behind) to raise the *single-relay*
   knee — which would lift every topology. Secondary: **distributed multi-host
   deployment (#342)** to convert the latency win into a real capacity multiplier,
   and re-check GSO on a real NIC (found off-path on WSL). **[INFERENCE — pre-registered
   as the next hypothesis, not yet tested.]**

## Environment limits & uncertainty (summary)
- **Single WSL2 host, all relays share 4 cores** → capacity multiplication is
  structurally unmeasurable; the flat result is expected and says nothing about
  real horizontal scaling. **The #1 gap is distributed deployment (#342).**
- Co-located load generator caps ~1500–2000 subscribers regardless of relay count.
- Resource matrix (`study-all.jsonl`) is pre-#348; current code (confirm-slo) is
  marginally better, same shape.
- 1200 B > real audio; absolute magnitudes environment-specific; the **shape**
  (latency relief real, single-host capacity flat) and the **model**
  (`fan-out ∝ subs/relay`) are the portable results.

### Repro
Multi-node: `cd bench-nomad && KLIST='0 1 2 4 8' RAMP='500,1000,1500,2000,3000,4000'
GPS=30 SIZE=1200 bash run-study.sh` (Nomad `raw_exec`, single host). Per-edge
pprof: `RELAY_PPROF=1` on the relay, `go tool pprof http://127.0.0.1:<edge>/debug/pprof/profile`.
Single-relay per-edge model: `FANOUT_KS=<N/K> FANOUT_GAP=33ms ./relay_mech.test …`.
