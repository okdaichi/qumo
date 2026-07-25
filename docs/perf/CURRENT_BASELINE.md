# qumo relay — Current Performance Baseline (audio realtime)

**Question this document answers:** *What is the guaranteed realtime-audio fan-out
capacity of one qumo relay node, and what changes improve it?*

**Date:** 2026-07-25 · **Code:** `main` post-#348/#349 (reusable `openTimeout`,
interleaved bench CI) · **gomoqt** `v0.16.2-0.20260718...7bc42f9`.

Every claim below is tagged **[FACT]** (measured this cycle or a cited prior cycle
against current code), **[INFERENCE]** (model-supported, not directly measured), or
**[UNKNOWN]** (needs the distributed harness #342). Lab ≠ production — see Caveats.

---

## 0. Workload definition (the audio baseline)

| Parameter | Value | Rationale |
|---|---|---|
| Frame cadence | 30 frames/s (gap ≈ 33 ms) | realtime audio equivalent |
| Frames per group | **1** | audio baseline = 1 object per group (group-open per frame) |
| Group open rate | 30 groups/s = 30 QUIC uni-streams/s | consequence of 1 frame/group |
| Frame size | 1200 B | conservative; real Opus ≈ 160 B, so this **overstates** load |
| Publisher | **paced**, not bursted | verified: group inter-arrival p50 = 33.7 ms |
| Topology | 1 publisher → 1 relay → N subscribers | single-node baseline |
| SLO | p99 ≤ ~300 ms, loss < 1 %, ≥ 0.95·N connected | realtime budget |

> This is deliberately **not** the video GOP workload (which groups 30–120 frames per
> stream and cuts p99 ~5×). It is also **not** the ~13 K idle-HOLD capacity number,
> which measured connection holding (gps=1, 64 B, recv-buffer bound), a different
> problem from active fan-out. See [`WORKLOAD-MODEL-REVIEW.md`], [`HOLD vs establishment`].

---

## 1. Benchmark inventory (task 1)

### Canonical baseline
- **`BenchmarkRelayChain_FanoutSingleRelay`** (`internal/relay/single_relay_bench_test.go`,
  `//go:build integration`). 1 pub → 1 relay → K subs, all in-process. Env knobs:
  `FANOUT_KS` (subscriber counts), `FANOUT_GAP` (pace), `FANOUT_FPG` (frames/group),
  `BENCH_DURATION`. Reports e2e p50/p95/p99, loss %, fps, heap, goroutines, and — with
  `-tags instrument` — a full per-stage latency table. **This is the fan-out baseline.**
- **`bench-nomad/`** (`run-study.sh` + `study.py`): out-of-process Nomad `raw_exec`
  cluster, relay pinned to cores 0–3, loadgen to 4–7, scrapes each relay's `/metrics`.
  **This is the isolated-relay + multi-node/topology harness** (the only way to read
  true relay CPU/RSS/GC without co-located load). Datasets: `study-all.jsonl` (K=1–8 ×
  N=500–4000), `confirm-slo.jsonl` (K=0,4,8 current code).

### Component-level (micro) benchmarks
- `quic_pps_bench_test.go` — `BenchmarkPureQUIC_PPS` (raw quic-go packet ceiling ≈ 222 K PPS).
- `processgroup_bench_test.go`, `egress_bench_test.go`, `benchmarks_test.go` — relay
  egress / `processGroup` / groupCache / broadcast / frame-pool micros.
- `multi_track_bench_test.go` — `BenchmarkRelay_MultiTrack` (rules out single-ingest limit).
- `open_timeout_test.go` — `BenchmarkOpenDeadline` (the #348 primitive).
- gomoqt module: `TrackWriter_OpenGroup`, Encode/Decode, field-coder/routing benches.

### Instrumentation / profiling
- **`//go:build instrument`** stage stamps (`stage_latency*.go`): A ingress / R ring
  residence (split R.fill vs R.wake) / O group-open / C write-frame + mechanism gauges
  (broadcast dur, concurrent deliveries, group inter-arrival). Zero-overhead default build.
- `RELAY_PPROF` opt-in pprof endpoint (#339); `go tool trace` sched-latency profile.

### What is missing / was the gap this cycle
- **Core-scaling of the active-fan-out knee** (GOMAXPROCS sweep) — *did not exist*;
  measured here (§5.2). Decisive for "add cores vs change the algorithm."
- **True horizontal capacity** across separate hosts (#342) — still **[UNKNOWN]**; the
  single-host cluster shares one 4-core budget, so capacity multiplication is unmeasurable.

**No new persistent benchmark was created** — the GOMAXPROCS gap was answered by
sweeping the existing canonical bench under varied `GOMAXPROCS`; everything else reuses
existing harnesses.

---

## 2. Unified baseline model

### 2a. qumo relay layer — subscriber scaling curve  [FACT]

In-process, 8 shared cores, current code, gap = 33 ms, 1 frame/group, 1200 B:

| Subs | e2e p50 | e2e p99 | loss % | fps (of 30) | streams/s | R.wake p50 | heap MB |
|---|---|---|---|---|---|---|---|
| 100 | 0.9 ms | 1.8 ms | 0 | 29.7 | ~3.0 K | 0.44 ms | 6 |
| 500 | 5.1 ms | 16.7 ms | ~0 | 29.9 | ~14.9 K | 1.20 ms | 26 |
| **1000** | 12.0 ms | 56–68 ms | 2.0 | 28.8 | ~28.8 K | 3.32 ms | 61 |
| **1500** | 30.5 ms | 304 ms | **46** | **16.0** | ~24 K | 9.9 ms | 62 |
| 2000 | 28.6 ms | 167 ms | **50** | **14.9** | ~30 K | 8.4 ms | 73 |
| 5000 | 222 ms | 1133 ms | **82** | **5.3** | ~26 K | 44.6 ms | 274 |

Isolated relay (Nomad K=0, pinned 4 cores, loadgen separate — pre-#348):

| Subs | conn | loss % | p99 | **relay CPU (of 4)** | RSS MB | goros/sess | GC max |
|---|---|---|---|---|---|---|---|
| 1000 | 992 | 0.2 | 227 ms | **1.88** | 981 | ~7 | 0.77 ms |
| 2000 | 1978 | 16 | ≥1000 ms | **2.75** | 2421 | ~7 | 1.6 ms |

**Headline [FACT]:** the single relay scales ~linearly to **~1000 subscribers**
(p99 ≤ ~56–68 ms, < 2 % loss, full 30 fps), then hits a **sharp knee between 1000 and
1500** where loss explodes (2 % → 46 %) and throughput halves. The knee is a
**positive-feedback collapse**: wake latency → misses frame budget → ring evicts →
loss. The isolated relay **never saturates its cores** (≤ 2.75 of 4 at N=2000), yet
still loses frames — so the limit is *not* CPU headroom.

### 2b. gomoqt layer — is it adding meaningful overhead?  [FACT]

- **Transport ceiling:** relay egress reaches ~242 K PPS on Linux ≈ the raw quic-go
  socket ceiling (~222 K, `BenchmarkPureQUIC_PPS`). gomoqt's per-object framing sits
  *under* the transport bound — **gomoqt is not the throughput limiter** at the socket.
- **Per-group allocation:** `processGroup` = 9 allocs/op (down from 11 via worker-pool
  #338). `deliverGroup`'s closures **do not escape** (verified `-gcflags=-m`, #335
  refuted). The reusable `openTimeout` (#348) removed the per-delivery
  `context.WithTimeout`: **272 B / 4 allocs → 0 / 0**, 57.8 ns/delivery (6.1× on the
  micro). gomoqt `EncodeWithStreamType` coalescing measured −14.3 % allocs on
  `TrackWriter_OpenGroup` (uncommitted study, offered as a gomoqt PR).
- **Group lifecycle is the dominant *per-frame* gomoqt cost** but not the *latency*
  driver: inside `deliverGroup`, CPU splits **61 % `OpenGroupAt` (open uni-stream) /
  27 % timer setup / `WriteFrame` not visible**. `OpenGroupAt` cost is real but it is
  **off the critical latency path** — the latency is queueing before egress runs, not
  the open itself (proven by openTimeout being latency-neutral, §4).

**Verdict [FACT]:** gomoqt overhead is allocation-shaped, not latency-shaped. It does
not explain the knee. The single biggest gomoqt lever is **amortizing stream churn with
larger groups** (video GOP) — explicitly out of scope for the audio baseline.

---

## 3. Bottleneck attribution — three separate categories (task 3)

### A. gomoqt bottlenecks
- `OpenGroupAt` per-frame stream open — **real cost, off critical path**; amortized only
  by larger groups (not applicable to 1-frame audio). [FACT]
- Allocations — largely closed (#338, #348); `EncodeWithStreamType` a further −14 %. [FACT]
- Serialization / stream ops limiting fan-out — **refuted**: multi-track flat, ingest µs. [FACT]

### B. qumo bottlenecks — **the real one**
- **Per-frame `broadcast()` O(N) wakeup (Case B).** One incoming frame wakes **all N**
  egress goroutines simultaneously → run-queue contention on a fixed core set. Confirmed
  by scheduler-latency profile: **51 % = `broadcastNotify.notify`**; R.fill (reserve→
  broadcast) is flat ~6–8 µs at all N, while R.wake (broadcast→pickup) scales 0.44 →
  44.6 ms with N. Groups are processed **in parallel, not serialized.** [FACT]
- Ring / groupCache / locks — **refuted** as bottleneck: lock-free append (#314), mutex
  profile 99 % test-harness, relay reserve 0.08 %. [FACT]

### C. environment bottlenecks
- **Co-located load (in-process bench):** relay + subs + publisher share 8 cores, so the
  in-process knee is a *combined-system* knee. The isolated relay only reaches 2.75/4
  cores — its true ceiling needs distributed load (#342). [FACT / caveat]
- **Single-host multi-node:** all relays pinned to the same 4 cores → adding edges
  subdivides a fixed budget (§5.3). Capacity multiplication is **structurally
  unmeasurable here.** [FACT]
- Windows is unreliable for quic-go stress → all stress runs cross-compiled to Linux/WSL2. [FACT]

---

## 4. Fan-out path, per-stage latency decomposition (task 4)  [FACT]

One frame's relay lifetime, stamped via the `instrument` build (N=1000):

```
publisher ──▶ ingest(A) ──▶ ring residence(R = R.fill + R.wake) ──▶ open(O) ──▶ write(C) ──▶ QUIC ──▶ sub
```

| Stage | What it measures | Behavior vs N | Owns latency? |
|---|---|---|---|
| A ingress | clone + RCU publish | flat, µs | no |
| **R.fill** | reserve → broadcast | **flat ~6–8 µs** | no |
| **R.wake** | broadcast → egress pickup | **scales 0.44 → 44.6 ms** | **YES** |
| O open | `OpenGroupAt` uni-stream | ~µs service, off critical path | no |
| C write | `WriteFrame` | flat µs | no |
| residual | quic-go send + wire | ~69 % of p99 (send/recv + sched) | partly |

**Where the 227 ms @ N=1000 goes [FACT]:** ~31 % thundering-herd egress drain
(R.wake = K × ~30 µs ÷ cores, a *service-rate* effect, not lock contention) + ~69 %
quic-go send/recv + scheduler residual. The latency is **waiting to be scheduled after
the broadcast wakes everyone**, not stream-open, write, GC, or locks.

---

## 5. Scaling dimensions (task 5)

### 5.1 Subscriber scaling
Linear to ~1000, sharp knee 1000–1500, collapse by ~2000 (§2a). [FACT]

### 5.2 Core scaling — **NEW this cycle**  [FACT]

GOMAXPROCS sweep, audio baseline, in-process, `BENCH_DURATION=8s`, single run/point:

| GOMAXPROCS | N=1000 fps | N=1000 loss % | N=1000 p99 | N=1500 fps | N=1500 loss % | N=1500 p99 |
|---|---|---|---|---|---|---|
| 2 | 17.0 | 41.7 | 180 ms | 12.2 | 59.4 | 328 ms |
| **4** | 28.5 | **1.2** | 84 ms | 23.2 | 21.0 | 122 ms |
| **8** | 28.8 | 2.3 | 68 ms | 26.0 | **9.3** | 136 ms |
| 16 | 27.7 | 2.9 | 94 ms | 24.7 | 12.9 | 150 ms |

**Does active-fan-out capacity scale with CPU cores? — No, it plateaus at ~4–8.** [FACT]
- 2 → 4 cores is **transformative** (N=1000 loss 41.7 % → 1.2 %; fps 17 → 28.5): below a
  threshold, the broadcast herd starves the run queue and the workload collapses.
- 4 → 8 → 16 gives **no further capacity** and marginally regresses (loss creeps 1.2 →
  2.3 → 2.9 %; p99 68 → 94 ms). Extra cores add scheduler overhead without relieving the
  per-frame O(N) wake fan-out.

**Consequence:** throwing hardware at the box is **not** the lever past ~8 cores. This is
consistent with (and extends) the isolated-relay finding that the relay never saturates
4 cores, and with the socket-ceiling being core-independent. The lever is the
**algorithm** (broadcast wake), the **topology** (hierarchy), or the **group size** (GOP).
*Caveat: in-process GOMAXPROCS scales pub+relay+subs together, and each point is a single
run — treat the plateau as the signal, not the exact per-point numbers.*

### 5.3 Topology scaling (hub → K edges)  [FACT]

From `study-all.jsonl` (single host, all relays pinned to cores 0–3):

| edges K | subs/edge @N=1000 | p99 @N=1000 | hub CPU | max sustainable subs | efficiency |
|---|---|---|---|---|---|
| 0 (single) | 1000 | **227 ms** | 1.88 | ~1000 | 100 % |
| 2 | 500 | 88 ms | — | ~1000 | 50 % |
| 4 | 250 | 45 ms | — | ~1500 | 38 % |
| 8 | 125 | **34 ms** | 0.05 | ~1500 | **19 %** |

- **Latency: hierarchy works [FACT].** p99 227 → 34 ms (7×) at N=1000 by cutting
  subscribers/edge 1000 → 125 — dividing the broadcast herd. Total relay CPU (~1.8 cores)
  is *conserved*; the hub becomes a ~0.05-core fan-in multiplexer.
- **Capacity: does NOT multiply on one host [FACT].** Max subs stays flat ~1000–1500;
  efficiency collapses 100 → 19 % because all edges share the same 4 cores. At the K=8
  ceiling the relays use only **1.78 / 4 cores — 56 % idle** (the co-located loadgen is
  the binder).
- **True K× capacity is [UNKNOWN]** — needs separate hosts (#342). Do **not** claim
  production horizontal scaling from colocated processes.

---

## 6. Reproducible commands (the loop)

```bash
# Cross-compile the integration bench for Linux (Windows is unreliable for quic-go stress)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -tags=integration \
  -o /tmp/relay_bench ./internal/relay/

# Subscriber sweep (audio baseline)
FANOUT_GAP=33ms FANOUT_FPG=1 FANOUT_KS=100,500,1000,1500,2000 BENCH_DURATION=8s \
  /tmp/relay_bench -test.run='^$' -test.bench=FanoutSingleRelay -test.benchtime=1x

# Core sweep (this cycle's new measurement)
for P in 2 4 8 16; do GOMAXPROCS=$P FANOUT_GAP=33ms FANOUT_FPG=1 FANOUT_KS=1000 \
  BENCH_DURATION=8s /tmp/relay_bench -test.run='^$' -test.bench=FanoutSingleRelay -test.benchtime=1x; done

# Per-stage latency attribution (instrument build)
go test -c -tags=integration,instrument -o /tmp/relay_instr ./internal/relay/
FANOUT_GAP=33ms FANOUT_KS=1000 /tmp/relay_instr -test.run='^$' -test.bench=FanoutSingleRelay -test.benchtime=1x

# Isolated relay + topology (out-of-process, true relay CPU/RSS/GC)
cd bench-nomad && KLIST=0,2,4,8 NLIST=500,1000,1500,2000 GPS=30 SIZE=1200 bash run-study.sh
```

Statistics: `benchstat` on ≥ 5 interleaved base/PR rounds (bench.yml #349); single-run
sweeps above are for shape, not regression gating.

---

## 7. Bottleneck summary & next optimization candidates

**Confirmed bottleneck [FACT]:** the qumo **per-frame `broadcast()` O(N) egress wakeup**
(Case B). Not relay CPU, not GC (2 %), not relay locks (~0), not the ring, not gomoqt
stream ops, not `WriteFrame`. At the audio baseline the guaranteed capacity of **one
relay ≈ 1000 active subscribers at p99 ≤ ~56–68 ms, < 2 % loss** — a scheduler-contention
ceiling reached before CPU saturation, and one that stops responding to added cores past ~8.

**Candidates, in priority order:**

1. **Attack the broadcast wakeup itself (root cause, code fix).** [INFERENCE — pre-registered]
   Reduce the simultaneous-wake cost: sharded/batched egress wakeup, or wake only
   subscribers actually behind, instead of closing/recreating a channel that wakes all N.
   *Hypothesis:* cuts R.wake growth → raises the single-relay knee → lifts **every**
   topology at once. *Refuted if:* R.wake stays ∝ N after the change.
2. **Hierarchy (deployment workaround, already validated for latency).** 227 → 34 ms.
   Not a capacity multiplier on one host; becomes one only across separate hosts (#342).
3. **Larger groups / GOP (gomoqt lever).** Amortizes `OpenGroupAt` stream churn ~5× —
   applies to video, not the 1-frame audio baseline.
4. **Distributed multi-host harness (#342).** Converts the hierarchy *latency* win into a
   real *capacity* multiplier, and is the only way to measure the true isolated-relay
   ceiling and post-hierarchy bottleneck (hub fan-in vs per-edge scheduler vs NIC PPS —
   currently **[UNKNOWN]**).

**Not worth pursuing (measured-negative):** ingest path, ring structure, GC tuning for
active fan-out, relay-lock sharding, `WriteFrame` micro-opt, per-subscriber `pending`
coalescing, F2 frame-copy elimination, egress counter sharding.

---

## Caveats (lab ≠ production)

- In-process bench co-locates publisher + relay + subscribers on shared cores; absolute
  knee numbers combine all three. Isolated-relay resource table is **pre-#348** (current
  code marginally better). GOMAXPROCS points are single-run.
- 1200 B frames overstate real audio (~160 B) → capacity here is **conservative**.
- All horizontal-capacity claims are single-host and therefore **latency-valid,
  capacity-inconclusive**; #342 is the standing gap.
- No production prediction is made from these lab numbers.

_Related: [`AUDIO-BASELINE-MODEL.md`], [`FANOUT-MECHANISM.md`], [`LATENCY-ATTRIBUTION.md`],
[`MULTI_NODE_SCALING.md`], [`WORKLOAD-MODEL-REVIEW.md`], [`OPTIMIZATION-LEDGER.md`]._
