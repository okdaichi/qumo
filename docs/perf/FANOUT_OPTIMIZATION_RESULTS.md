# Fan-out Optimization Exploration — Results

**Cycle goal:** improve active-audio fan-out scalability of one qumo relay.
**Date:** 2026-07-25 · **Base commit:** `2b5200e` (v0.5.0) + uncommitted stage
instrumentation · **Method:** perf-engineer discipline (measure → profile →
hypothesize → adjudicate). **Outcome:** **no fan-out code change is warranted** —
the stated root cause is refuted by direct measurement. This is a *no-change*
result, which the discipline treats as a success: the exploration determined that
no meaningful opportunity exists **in the broadcast/egress path**, and re-localized
the real cost to **per-connection QUIC transport**.

Tags: **[FACT]** measured this cycle · **[REFUTED]/[ADOPT]/[REVISIT]** decision.

---

## 0. The premise being tested

The brief states the root cause as: *"per-frame `broadcast()` wakes all subscriber
goroutines; R.wake scales ~O(N)."* Candidates 1–4 all attack that wakeup. **Before
implementing any of them, this cycle measured whether the wakeup is actually the
cost.** It is not.

---

## 1. Baseline (audio: 30 fps, 1 frame/group, 1200 B)  [FACT]

From [`CURRENT_BASELINE.md`] / [`AUDIO-BASELINE-MODEL.md`], current code:

| Subs | p50 | p99 | loss % | R.wake p50 |
|---|---|---|---|---|
| 1000 | 12.0 ms | 56–68 ms | 2.0 | 3.32 ms |
| 1500 | 30.5 ms | 304 ms | 46 | 9.9 ms |
| 5000 | 222 ms | 1133 ms | 82 | 44.6 ms |

R.wake (broadcast → egress pickup) is the term that grows with N. The question is
**why** — and every candidate design depends on the answer.

---

## 2. Decisive pre-implementation measurements

### 2a. Cost of the wakeup primitive itself — standalone prototype  [FACT]

A pure-scheduling model (no quic-go): park N goroutines, then time the wake path.

**`close(ch)` with N parked waiters** (the `broadcastNotify.notify` goready walk):

| N | 100 | 500 | 1000 | 1500 | 2000 |
|---|---|---|---|---|---|
| close time | ~0 | ~0 | ~0 | 2.5 µs | ~0 |

**Publish → all N goroutines woken *and* did a tiny fixed work** (run-queue drain,
GOMAXPROCS=8):

| N | per-goroutine (current model) | worker-pool M=8 | M=16 | M=32 |
|---|---|---|---|---|
| 1000 | **12.9 µs** | ~0 | ~0 | ~0 |
| 2000 | **143 µs** | ~0 | ~0 | ~0 |

**Reading [FACT]:** closing a channel with 1000–2000 waiters is **~0**; waking and
scheduling all of them to completion is **tens of microseconds**. The wakeup and
the goroutine-count are **µs-scale**. The real relay's R.wake is **3–44 ms** — a
1000× gap the wakeup cannot explain.

### 2b. Where the milliseconds actually are — knee CPU profile  [FACT]

`bench-lat/cpu_knee.pprof` (N=1500, 84.49 s total):

| Region | cum % | |
|---|---|---|
| **`broadcastNotify` (notify + close + lazyInit)** | **0.047 %** | ← the premise |
| quic-go `(*Conn).run` (per-connection event loop) | **30.5 %** | transport |
| quic-go `sendQueue.Run` → `sconn.writePacket` | 18.8 → 16.8 % | transport |
| `Syscall6` / `SendmsgN` (UDP sends, ~30 K pps) | **16.0 / 12.9 %** | transport |
| quic-go `packetPacker.appendPacket` + AES | ~10 % | transport/crypto |
| relay `deliverGroup` | 11.2 % | (of which `OpenGroupAt` only ~3 %) |
| runtime scheduler (futex/selectgo/schedule/lock2) | ~40 % flat | churn of N conns |

**The entire broadcast path is 0.047 % of CPU.** The cost is **N independent QUIC
connections**, each running its own `Conn.run` loop, packet-packing, crypto, and
`sendmsg`. The ~40 % runtime-scheduler time is the churn of managing ~1000
connection goroutines — **not** the 1000 egress goroutines (which are a µs-scale
subset, per 2a).

**Conclusion [FACT]:** R.wake is inflated because a woken egress goroutine competes
for cores with ~1000 quic-go connection goroutines doing real send work — the
latency is **downstream transport**, triggered by the broadcast, not caused by it.

---

## 3. Candidate adjudication

### Candidate 1A — Sharded broadcast channels  **[REFUTED]**
- *Hypothesis:* splitting the notify channel into M reduces the wakeup herd.
- *Measurement:* `close()` with N waiters is ~0 (2a); the whole notify path is
  0.047 % (2b). Sharding parallelizes a **zero** cost. Sharding the *channel* also
  does **not** reduce the number of goroutines woken (all subscribers need every
  audio frame) — it does M closes instead of 1.
- *Decision:* **REJECT.** Attacks a non-cost. Not implemented.

### Candidate 1B / 2 — Batched drain / worker-pool egress (fewer goroutines)  **[REFUTED]**
- *Hypothesis:* replacing N parked egress goroutines with M workers cuts
  scheduler pressure and R.wake.
- *Measurement:* waking N=2000 goroutines to completion is 143 µs; worker-pool is
  ~0 — a **µs** saving. The ~40 % scheduler time in the real profile is dominated
  by the **~1000 quic-go connection goroutines**, which a worker-pool does **not**
  remove (each connection still needs its own event loop + `sendmsg`). Meanwhile a
  worker serving N/M subscribers sequentially re-introduces **head-of-line
  blocking** when one subscriber's `OpenGroupAt` hits MAX_STREAMS backpressure —
  the exact isolation the per-subscriber-goroutine design provides.
- *Decision:* **REJECT** (predicted-negative, high correctness risk). Not built. A
  real-relay A/B could close it fully, but the profile shows the target (egress
  goroutine churn) is a minority of scheduler time; prediction is negative.

### Candidate 1C — Wake only subscribers that need data  **[REFUTED for audio]**
- *Hypothesis:* skip waking caught-up subscribers.
- *Measurement:* in steady-state audio **every** subscriber is caught up and needs
  **every** frame (30 fps, 1 frame/group). "Only those behind" = everyone. The
  egress loop already skips redelivery via the notify seq-guard.
- *Decision:* **REJECT** for the audio baseline. (The mechanism only exists for a
  transient "behind" subscriber, already handled.)

### Candidate 2 (dispatcher/worker variants) — see 1B. **[REFUTED]**
The target metric is p99; fewer runnable goroutines saves µs, not the ms in transport.

### Candidate 3 — Reduce goroutine scheduler pressure (GOMAXPROCS, pinning)  **[REFUTED as a lever]**
- *Measurement (prior cycle, [`CURRENT_BASELINE.md` §5.2]):* capacity **plateaus at
  ~4–8 cores** — GOMAXPROCS 2→4 is transformative, 4→8→16 gives nothing and
  marginally regresses. `LockOSThread`/pinning would fight the ~1000-connection
  scheduler churn that is inherent to N connections.
- *Decision:* **REJECT.** Hardware/scheduler tuning is not the lever past ~8 cores.

### Candidate 4 — Reduce per-frame fan-out work (copies/atomics/locks/alloc)  **[REFUTED]**
- *Measurement:* mutex profile ~0 relay contention (99 % harness); GC 2 %;
  lock-free ring (#314); `deliverGroup` closures don't escape (#335); `openTimeout`
  already removed the per-delivery `WithTimeout` alloc (#348). The fan-out
  orchestration is already lean; it is 0.047 %–11 % of CPU and none of it is the ms.
- *Decision:* **REJECT.** Nothing material left in the relay-side per-frame path.

### Candidate 5 — gomoqt transport (OpenGroupAt, stream churn)  **[REFUTED as primary]**
- *Measurement:* `OpenGroupAt` = ~3 % cum at the knee; gomoqt is thin over quic-go.
  The cost is quic-go `Conn.run` + `sendmsg`, below gomoqt.
- *Decision:* **REJECT as the fan-out bottleneck.** (Allocation micro-wins like
  `EncodeWithStreamType` −14 % remain nice-to-have, not latency-relevant.)

### Candidate 6 — Group model (audio 1/group vs video GOP)  **[REVISIT — video only]**
- *Measurement (prior):* GOP grouping (FPG 30–60) cuts p99 233→45 ms (~5×) by
  amortizing stream-open churn. But the **audio baseline is 1 frame/group by the
  publisher** — the relay forwards groups as-is and cannot re-group.
- *Decision:* **REVISIT for the video workload**; out of scope for audio here.

### Candidate 7 — Network/kernel (GSO, socket buffers)  **[REVISIT — environment]**
- *Measurement:* `Syscall6`/`SendmsgN` = ~16 % — the single largest addressable
  cost. GSO (UDP segmentation offload) batches many sends into one syscall. **But
  quic-go v0.60 exposes no public GSO knob** (it auto-detects `UDP_SEGMENT`), and it
  was found **off-path on WSL2**. It is an environment/kernel capability, not a
  qumo/gomoqt code change ([`OPTIMIZATION-LEDGER.md`], [`quic_go_no_public_bandwidth_api`]).
- *Decision:* **REVISIT on bare-metal Linux** where GSO is active; not actionable in
  qumo code today.

---

## 4. What actually improves audio fan-out

Ranked by profile support, only two levers survive — and neither is a fan-out-code change:

1. **Hierarchy / topology (deployment)** — **[ADOPT, already validated].** Fewer
   connections per relay → proportionally less `Conn.run` (30 %) + `sendmsg` (16 %).
   Measured p99 227 → 34 ms at N=1000 with K=8 edges ([`MULTI_NODE_SCALING.md`]). This
   is the direct remedy for a per-connection-transport bottleneck.
2. **GSO on bare-metal Linux (environment)** — **[REVISIT].** Attacks the 16 %
   syscall directly; no qumo knob in quic-go v0.60; verify on a real NIC (#342 host).

**Everything in the broadcast/egress-goroutine family is refused on evidence** — it
targets 0.047 % of the cost.

---

## 5. Why the premise was wrong (and how to prevent recurrence)

The belief that `broadcast()` is O(N)-expensive traces to
`BenchmarkBroadcastNotify_Notify` / `BenchmarkTrackDistributor_Broadcast`, which by
design run with **zero live waiters** (the code comment: *"There are no live waiter
goroutines by design"*). A waiter-free `close()` is O(1); the concern was that a
waiter-*ful* close is O(N). This cycle measured the waiter-ful close directly: it is
**still ~0** (goready is nanoseconds per waiter). The premise conflated "goroutines
woken by notify" with "goroutines whose downstream quic-go work is slow" — the
sched-latency profile attributes the *downstream* wait to the *wake point*.

**Constructive artifact (offered, not landed):** add a **waiter-ful broadcast
benchmark** (N goroutines actually parked) to the default build. It closes the
measurement gap, empirically guards that the wakeup stays cheap, and prevents future
misattribution. It is the only code change this cycle would justify — and it is a
*guard*, not an optimization. Note: as a new benchmark it has no base to compare
against in the `performance-check` CI (benchstat needs a baseline on both sides), so
its value is the absolute numbers, not a base-vs-PR delta.

---

## 6. Process notes

- **No worktrees/PRs were opened.** Every code candidate was pre-refuted by
  measurement (§2), so implementing them in isolated worktrees would produce
  guaranteed no-ops — the discipline says revert no-ops, so they were never written.
- **`performance-check` CI scope caveat:** that workflow runs `go test -bench` with
  **no build tags**, so it measures only default-build **component microbenchmarks**
  (via benchstat), *not* the integration-tagged `FanoutSingleRelay` bench or the
  instrument stage metrics. It cannot, as configured, measure fan-out p99/R.wake — a
  fan-out change must be validated with the integration+instrument bench on Linux/WSL.
- **Lab ≠ production:** absolute numbers are WSL2/in-process; the *bottleneck class*
  (per-connection transport, `sendmsg`) is portable. No production prediction is made.
- Working tree unchanged (no relay code modified this cycle).

---

## 7. Summary decision table

| Candidate | Target | Evidence | Decision |
|---|---|---|---|
| 1A sharded broadcast | notify herd | close ~0; notify 0.047 % | **REJECT** |
| 1B/2 worker-pool egress | goroutine count | drain µs; +HOL risk; conn-goroutines untouched | **REJECT** |
| 1C wake-only-behind | skip wakeups | all subs need every audio frame | **REJECT** |
| 3 scheduler/GOMAXPROCS | run-queue | core-scaling plateaus ~8 | **REJECT** |
| 4 per-frame fan-out work | copies/locks/alloc | locks ~0, GC 2 %, already lean | **REJECT** |
| 5 gomoqt OpenGroupAt | stream open | ~3 % at knee | **REJECT (primary)** |
| 6 group model / GOP | stream churn | 5× win but audio=1/group | **REVISIT (video)** |
| 7 GSO / kernel | 16 % sendmsg | no quic-go knob; off-path on WSL | **REVISIT (bare metal)** |
| Hierarchy (topology) | N conns/relay | 227→34 ms measured | **ADOPT (deployment)** |

_Related: [`CURRENT_BASELINE.md`], [`LATENCY-ATTRIBUTION.md`], [`FANOUT-MECHANISM.md`],
[`MULTI_NODE_SCALING.md`], [`OPTIMIZATION-LEDGER.md`]._
