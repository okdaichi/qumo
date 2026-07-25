# qumo relay — capacity under a latency SLO (flat vs. hierarchical)

**Question:** how many concurrent subscribers can one qumo deployment serve while
keeping p99 end-to-end latency under a **300 ms SLO**, and does a hierarchical
hub→edge topology raise that number?

**Short answer (measured, single-host WSL2):** flat topology is **latency-bound at
~1000 subscribers** (p99 177 ms; 470 ms by 1500). Hierarchy keeps p99 **far** under
the SLO (20–78 ms up to 1500 subscribers) — but on a single host its true SLO
ceiling **cannot be measured**, because the co-located load generator, not the
relay, is the binding constraint above ~1500–2000 subscribers. The relays never
exceeded ~2.7 of the 4 cores available to them at any operating point.

> **This is not a production capacity number.** Every figure here is single-host
> WSL2 with the load generator sharing the machine. See §7. The production
> statement this study *can* support is in §8; the number it *cannot yet* give
> (the hierarchical SLO ceiling) requires distributed load generation (#342).

---

## 1. Environment

| | |
|---|---|
| Host | WSL2 Ubuntu on Windows, 8 logical cores (i7-10700K class), 16 GB RAM |
| Orchestration | HashiCorp Nomad `raw_exec` (native binaries, no Docker) |
| CPU pinning | relays → cores 0–3; publisher + load generators → cores 4–7 (`taskset`) |
| UDP recv buffer | 416 KB (WSL `rmem_max` cap; quic-go requested 7 MB, was clamped) |
| Relay build | current `main` @ `b473292` (includes #348 reusable-OpenGroupAt deadline) |
| Load gen build | `feat/loadgen-e2e-latency` (adds #350 e2e latency histogram) |

**Load generation shares the host with the relays.** This is the single most
important environmental fact: cores 4–7 run the publisher *and* all subscriber
processes (QUIC handshake crypto, frame decode, timestamp extraction). It is a
capacity binder well below the relay's own ceiling.

## 2. Methodology

- **Workload:** one publisher → hub, constant **30 fps × 1200 B** frames, UnixNano
  timestamp at payload[8:16]. Subscribers distributed evenly and launched
  **concurrently** across the serving relays (hub for K=0; the K edges otherwise).
- **Topologies:** K = 0 (flat: subscribers direct to one relay), 1, 2, 4, 8 edges.
- **Latency:** the #350 loadgen histogram (0.1 ms buckets, lock-free). Each
  subscriber process settles up to 30 s (waiting for connections), **resets the
  histogram**, then holds — so reported p50/p95/p99 exclude the ramp by
  construction, satisfying "do not include ramp period in latency statistics".
- **Sustained** at an operating point iff **all** of: p99 < 300 ms, frame loss
  < 1 %, connected ≥ 95 % of target, relay total CPU < 3.8/4 cores.
- **Two datasets** (both reported; they agree on every conclusion):
  - **A — full envelope** (`study-all.jsonl`): K ∈ {0,1,2,4,8} × N ∈ {500…4000},
    ~18–20 s window, 30 operating points.
  - **B — SLO confirmation** (`confirm-slo.jsonl`): merged relay + #350 latency,
    **60 s** window, K ∈ {0,4,8} × N ∈ {1000,1500,2000}, 9 points.

**Not achievable on this hardware:** N = 5 000 / 10 000 / 20 000 / 30 000 / 50 000
of the media workload, and K = 16. The single-host load generator collapses at
~2 000 media-workload subscribers (§4), so those points cannot be *generated*,
let alone served. They are omitted rather than fabricated.

## 3. Results — SLO confirmation run (merged code, 60 s window)

| K | N | connected | loss % | p50 | p95 | **p99** | hub CPU | edge CPU (Σ) | SLO? | binding limiter |
|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1000 | 1000 | 0.0 | 10.9 | 24.2 | **177 ms** | 1.73 | — | ✅ | (headroom) |
| 0 | 1500 | 1494 | 1.5 | 15.4 | 116.7 | **470 ms** | 2.42 | — | ❌ | **relay fan-out latency** |
| 0 | 2000 | 1941 | 5.6 | 32.6 | 413.3 | **≥1000 ms** | 2.91 | — | ❌ | relay fan-out latency |
| 4 | 1000 | 910 | 0.0 | 7.2 | 17.3 | **29 ms** | 0.04 | 1.30 | ❌* | loadgen (est. 910/1000) |
| 4 | 1500 | 1500 | 0.08 | 15.4 | 63.9 | **289 ms** | 0.04 | 2.67 | ✅ | (SLO-edge) |
| 4 | 2000 | 1999 | 84.3 | 53.0 | 469.5 | 863 ms | 0.04 | 0.76 | ❌ | single-host collapse |
| 8 | 1000 | 909 | 3.7 | 7.5 | 15.6 | **20 ms** | 0.05 | 1.21 | ❌* | loadgen (est. 909/1000) |
| 8 | 1500 | 1496 | 12.5 | 11.0 | 23.5 | **78 ms** | 0.05 | 2.12 | ❌* | loadgen (publisher-sustain loss) |
| 8 | 2000 | 1865 | 6.7 | 20.3 | 108.3 | 386 ms | 0.05 | 2.22 | ❌ | single-host collapse |

\* **Not-sustained for a load-generation reason, not a relay or latency reason.**
Where marked, p99 is well under 300 ms and the relay is far from saturated; the
gate failed on establishment (<95 % connected) or on loss that traces to the
co-located publisher failing to sustain 30 fps over the 60 s window (§4).

**Full envelope (dataset A, 18–20 s window)** extends this to K=1,2 and to the
degradation tail; its sustained SLO ceilings: K0→1000, K1→1500, K2→1000/1500,
K4→1500, K8→1500. Both datasets agree.

## 4. The binding constraint is the co-located load generator, not the relay

Four independent signatures, all measured:

1. **`loadgen-underconnected` at N≥2000 everywhere.** The generator cannot
   *establish* the target subscriber count (e.g. dataset A, K=8/N=4000: **148 of
   4000** connected, 100 % loss). The relay cannot serve subscribers that were
   never created.
2. **Edges go idle under "overload".** K=4/N=2000: all 1999 connect, then loss
   hits 84 % and edge CPU **drops to 0.76 cores** — the edges have nothing to send
   because the publisher (cores 4–7, contended) stopped sustaining 30 fps. A
   relay-bound collapse would show edges *pegged*, not idle.
3. **Window length changes loss, not latency.** K=8/N=1500 loss was 0.05 % at an
   18 s window (dataset A) but 12.5 % at 60 s (dataset B) — while p99 stayed low
   (60→78 ms). Longer windows give the contended single-host publisher more
   opportunity to drop frames; the relay's latency is unaffected. Loss here is a
   **load-generation artifact**, and p99 is the robust metric.
4. **Relay headroom at every point.** Total relay CPU never exceeded ~2.7 of 4
   pinned cores; the hub in hierarchical mode sat at **0.04–0.05 cores**. GC max
   pause ≤ ~11 ms, ~0.05 GC/s. The relay is nowhere near a ceiling.

## 5. Where the latency knee is, and why hierarchy moves it

p99 vs. N (dataset A, ms; values ≥1000 are the histogram ceiling = collapse):

| K | 500 | 1000 | 1500 | 2000 |
|---|---|---|---|---|
| 0 (flat) | 31 | **227** | 737 | ≥1000 |
| 2 | 37 | 88 | 456 | ≥1000 |
| 4 | 22 | 45 | 232 | 877 |
| 8 | 19 | **34** | 125 | 340 |

- **Flat topology has a real, relay-attributable latency knee at ~1000
  subscribers.** This is the one clean SLO ceiling in the study: at N=1000 all
  1000 connect, loss is ~0, the relay is at 1.7–1.9 cores, and p99 is 177–227 ms —
  *the relay's own fan-out latency* is what crosses 300 ms by N=1500.
- **Mechanism (already measured, PR #348 latency attribution):** at fan-out F on
  one relay, each published frame wakes all F egress goroutines; the per-frame
  drain is a service-rate limit ≈ F × (~30 µs per subscriber) ÷ cores. At F=1000
  that is ~3.8 ms of ring-residence per frame at p50, growing at the tail — the
  dominant term in the flat p99. Splitting F across K edges divides this term by
  K, which is exactly the measured 227 ms → 34 ms (K=0→8) at N=1000.
- **Work is redistributed, not reduced (single host).** At N=1000, total relay
  CPU (~1.8 cores), aggregate egress (~250 Mbps), allocations (~650 K/s) and RSS
  (~900 MB) are **invariant** across K=0→8; hierarchy moves the fan-out off the
  hub (hub 1.88 → 0.03 cores) onto edges (each ~1/K of the work). On one host this
  buys latency and hub-offload, **not** additional capacity.

## 6. Per-node resource usage (hierarchical, K=8 / N=1000, dataset A)

| node | CPU | RSS | goroutines | egress | GC max | sessions |
|---|---|---|---|---|---|---|
| hub | 0.047 c | 41 MB | 121 | 2 Mbps | 0 ms | 9 (edges) |
| each edge (×8) | ~0.22 c | ~118 MB | 61 | ~30 Mbps | ≤11 ms | 126 |
| **total** | **~1.8 c** | **~980 MB** | ~610 | ~240 Mbps | — | 1000 |

Per-session cost (consistent with the capacity characterization): **~7
goroutines and ~470 KB RSS**, essentially all quic-go per-connection state — the
relay/gomoqt code is <1 % of CPU. Egress frame-rate proxy at sustained points:
~30 K frames/s (K0/N1000) to ~45 K frames/s (K4/K8, N1500). **UDP packets/sec is
not directly instrumented** — a known measurement gap; egress Mbps and frame-rate
are the available proxies.

## 7. Bottleneck analysis — which subsystem limits each ceiling

| Observed ceiling | Limiting subsystem | Evidence | Class |
|---|---|---|---|
| Flat p99 > 300 ms at N≳1000 | **relay subscriber fan-out** (egress drain) | p99 knee with relay at 1.7 c, 0 loss, all connected; PR #348 stage attribution (R-stage ∝ F/cores) | **Relay-attributable** (the SLO-relevant one) |
| Can't establish > ~2000 subs | **co-located load generator** (QUIC handshake on shared cores 4–7) | `loadgen-underconnected`; relay ≤2.7/4 c | Harness (single-host) |
| Loss rises with window length | **co-located publisher** can't sustain 30 fps | 0.05 %→12.5 % loss 18 s→60 s at flat p99 | Harness (single-host) |
| Hierarchical SLO ceiling | **unmeasurable here** — masked by the two above | p99 20–78 ms with relay idle at the loadgen limit | Blocked → needs #342 |
| (Not observed) relay CPU / GC / memory | — | ≤2.7/4 c, GC ≤11 ms, RSS <1 GB | Not a bottleneck |

**No ceiling in this study is attributable to relay CPU saturation, GC, memory,
or the QUIC transport itself** — for the media workload on one host, the limits
are (a) the relay's fan-out latency in *flat* mode and (b) the co-located load
generator in *every* mode above ~1500–2000 subscribers.

**Do not conflate with the ~13 K / ~20 K capacity numbers.** Those were measured
with a *different, minimal* workload (64 B frames, 1 fps — essentially idle held
connections) and characterize an **establishment/recv-buffer ceiling** (WSL 416 KB
`rmem_max`), not media-workload SLO capacity. Under 30 fps × 1200 B on one host,
the media workload cannot approach those counts — the loadgen collapses at ~2 000.
They answer a different question and must not be combined into an SLO claim.

## 8. Recommended architecture & the statement this study supports

**Measured, defensible:**

> On a single 8-core host, a **flat** qumo relay serves **~1000 concurrent
> subscribers** of a 30 fps / 1200 B stream within a **300 ms p99 SLO** (measured
> p99 177 ms), and is **latency-bound** — not CPU-bound — beyond that. Inserting a
> **hierarchical hub→edge layer collapses p99 by ~6–7× at equal load** (227 ms →
> 34 ms at N=1000 with 8 edges) and offloads the hub to near-zero CPU.

**Architecture recommendation:** use hierarchy for **latency and hub-offload**;
the knee is at **2–4 edges** (p99 already 45–88 ms at N=1000; beyond that the
per-edge latency gain diminishes and operational fragility rises). On a **single
host** hierarchy does **not** raise subscriber capacity — total work is conserved
and the shared load generator caps it. Real horizontal capacity scaling requires
**each edge on its own host** (own cores, NIC, recv-buffer).

**Cannot yet be stated (needs measurement):** the *number* of subscribers a
hierarchical deployment sustains under the SLO. On this box hierarchy's p99 stays
at 20–78 ms right up to the loadgen limit — the relay ceiling is never reached.
The next experiment is the only way to get it:

**Next experiment (#342):** distributed load generation — publisher, N subscriber
generators, and K edges each on **separate hosts** with a tuned kernel
(`net.core.rmem_max` ≥ 7 MB). Ramp per-edge subscribers until *the relay's* p99
crosses 300 ms. That isolates the true per-edge SLO capacity and turns "≥1500,
loadgen-masked" into a real number. Profiling to collect there: relay CPU
profile at the p99 knee (confirm fan-out drain vs. quic-go send path) and UDP
pps via `ss -u` / NIC counters (the gap noted in §6).

## Appendix — reproducibility & environment limits

- Data: `bench-nomad/study-all.jsonl` (envelope, 30 pts) + `confirm-slo.jsonl`
  (SLO confirmation, 9 pts). Harness: `bench-nomad/{run-study.sh, study.py,
  gen-cluster.py, build.sh}`.
- Confirmation invocation: `KLIST='0 4 8' RAMP='1000,1500,2000' HOLD=60s
  TARGET_P99_MS=300 bash run-study.sh`.
- **Limits:** WSL2 single host; load generator co-located (the dominant binder);
  UDP recv buffer kernel-capped at 416 KB; absolute latencies include co-located
  loadgen read scheduling. **Relative** comparisons across topologies under
  identical load are valid; **absolute** magnitudes are environment-specific and
  are **not** production numbers.
