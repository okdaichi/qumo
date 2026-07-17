# Single-node relay capacity: a GPS-driven methodology

**Goal:** characterize qumo's architectural scalability — can one node sustain many
concurrent subscriber Sessions at useful Group rate — in a way that is
application-agnostic and comparable to other relay/CDN systems. This characterizes
the *architecture*; it does not assume the bottleneck and does not validate any
single workload.

This document is the methodology. The benchmarks that implement it are in
`capacity_bench_test.go`; the per-stage instrumentation that *explains* results is
behind `//go:build instrument` (`stage_latency*.go`).

---

## 1. Terminology (precise — use these, not loose synonyms)

- **Session** = one QUIC/WebTransport connection. The **primary scaling variable**
  is the number of concurrent Sessions (do **not** use "subscriber count" as the axis).
- **Track** = a subscription within a Session. (Fixed at **1 Track per Session** for
  now; it becomes an axis later.)
- **Group** = one MoQ Group. In this stack one Group = one QUIC uni-stream, so
  "Groups/sec" *is* "stream-creation/sec" *is* "stream churn."
- **GPS** = Groups created per second within one Track in one Session.
- **FramesPerGroup (F)** = frames written into each Group.
- **FPS = GPS × FramesPerGroup** — a **derived** quantity, NOT an input.

Workload shape: **P publisher Sessions**, **S subscriber Sessions**, each
subscribing to one Track of the same broadcast. P=1 unless studying ingest scaling.

---

## 2. The reframe: GPS is a first-order axis (FPS is not)

The prior benchmark model treated FPS as the primary workload axis. Measurement
showed this is wrong. At **matched packet rate** (S × FPS constant), changing only
`FramesPerGroup` — i.e. changing GPS while holding packet rate ~constant —
**nearly doubled throughput** and flipped SUSTAINED↔NOT, while every relay
code-level optimization measured as negligible:

| (S=256, FPS=100) | GPS | delivered | ratio | verdict |
|---|---|---|---|---|
| F=1 | 100 | 13.0K pps | 0.51 | NOT-SUSTAINED |
| F=10 | 10 | 24.4K pps | 0.96 | SUSTAINED |
| F=30 | 3.3 | 24.7K pps | 0.97 | SUSTAINED |

Mechanism: every Group opens then closes one QUIC uni-stream. Groups within a
Track are delivered **sequentially** (one at a time; ≤3–5 concurrent under jitter),
so the subscriber's ~100 `MaxIncomingUniStreams` is loose headroom and is **not**
the binding constraint. What GPS drives is the **per-Group stream-object
open/close rate** — quic-go allocates a SendStream/ReceiveStream/frameSorter per
Group and tears it down on close, and that lifecycle repeated at GPS rate (plus
the framing/control work per Group) is the churn cost. This **stream-lifecycle-rate
ceiling is lower than the packet/socket ceiling**, so it binds first. **GPS is a
first-order scalability axis; FPS is derived.**

From now on the methodology and every optimization campaign are **driven by GPS**,
not FPS.

---

## 3. Define "sustain" — the predicate

A cell `(Sessions, GPS, F, FrameSize)` is **sustained** iff, in steady state
(after warmup, over `T ≥ 30 s`):

1. **delivery ratio ≥ 0.95** — subscribers receive ≥ 95% of offered frames
   (offered FPS = GPS × F),
2. **p99 end-to-end latency ≤ τ** (default τ = 250 ms),
3. **no monotonically growing backlog**.

**Loss% is a trap.** A relay reports ~0% loss by backpressuring the publisher to a
crawl; `loss% = (written − delivered)/written` is denominated on what the publisher
*actually wrote* (after backpressure), so it stays ~0 while delivery collapses.
**Delivery ratio (delivered / offered)** is the robust metric. The benchmark also
reports a **group-write ratio** (groups the publisher got through / offered): if
write-ratio < 1 the **publisher was backpressured**; if write-ratio ≈ 1 but
delivery-ratio < 1 the **relay itself dropped**. This split keeps us from assuming
which side failed.

---

## 4. The four scalability axes and their ceilings

The relay has **four** (largely independent) ceilings. The methodology isolates
each by holding the others minimal and sweeping one:

| Axis | Driven by | What saturates | Isolating probe |
|---|---|---|---|
| **Sessions — Establishment** | burst connection rate | handshake-accept rate, scheduler under burst | A-burst — all-at-once dial |
| **Sessions — Hold** | concurrently maintained | per-Session state, goroutines, memory, scheduler at steady state | A-ramp — controlled-rate dial |
| **GPS** (stream churn) | Groups/sec per Track | per-Group stream-object open/close rate (lifecycle) | B — sweep GPS at F=1, small B |
| **PPS** (packet/socket) | aggregate packets/sec | single UDP socket `sendmsg` (core-independent) | C — sweep F at GPS=1, small B |
| **Bandwidth** | aggregate bytes/sec | NIC + AES-GCM byte throughput | D — sweep FrameSize at low GPS |

**Establishment ≠ Hold.** A node may fail to *burst-connect* 10K Sessions
(handshake/scheduler under simultaneous arrival) while still *holding* 10K
Sessions that arrived gradually. These are distinct architectural properties —
measure both. **Do not conclude "Sessions bind before GPS" from the burst probe
alone** — that shows the burst-establishment ceiling, not the steady-state hold
ceiling.

A configuration is bounded by whichever ceiling is lowest. The levers that move
each ceiling are different (Sessions→ per-conn overhead in gomoqt/quic-go; GPS→
grouping; PPS→ socket/GSO; Bandwidth→ NIC/crypto), so reporting "which axis binds"
*is* the architectural characterization.

---

## 5. Performance objectives (what to maximize/report)

Goal is **not** max FPS. Report, per cell:

- **sustainable GPS per Session**, **aggregate GPS**, **max concurrent Sessions**,
- delivery ratio, group-write ratio, p99 latency,
- CPU (per core), memory (RSS), goroutines,
- aggregate PPS, aggregate bandwidth.

The capacity frontier is "max sustainable GPS at each Session count" (and the
three other ceilings), not a single number.

---

## 6. The MINIMUM matrix (isolating probes — not the full grid)

The full grid (Sessions × GPS × F × FrameSize) is ~1000+ cells and mostly
redundant. The **minimum matrix** exposes every bottleneck by isolating one axis
per probe. Each probe is a 1-D sweep; run one process per cell.

| Probe | Isolates | Sweep | Hold minimal |
|---|---|---|---|
| **A1. Session-carry (burst)** | Sessions **Establishment** ceiling | S = 64 → 10000, dial all at once | GPS=0.5, F=1, B=64B |
| **A2. Session-hold (slow ramp)** | Sessions **Hold** ceiling | ramp S at `RAMP_SESSIONS_PER_SEC` → target | GPS=0.5, F=1, B=64B |
| **B. GPS sweep** | GPS (churn) ceiling | GPS = 0.5 → 100 | S fixed, F=1, B=64B |
| **C. FramesPerGroup sweep** | PPS (socket) ceiling | F = 1 → 100 | S fixed, GPS=1, B=64B |
| **D. FrameSize sweep** | Bandwidth ceiling | B = 64B / 1200B / 16KB | S fixed, GPS=1, F=moderate |

**The diagnostic pair (B vs C):** probes B and C can reach the **same aggregate
PPS** by different paths — B via high GPS (F=1), C via high F (GPS=1). At matched
PPS, if B delivers less than C, the system is **GPS/churn-bound**; if equal, it is
**PPS/socket-bound**. This single comparison separates the two most-confused
ceilings. (This is exactly what revealed that the prior "~socket ceiling" was a
churn artifact.)

**Plus one interaction sweep:** Sessions × GPS at a few points (does the GPS
ceiling fall as Sessions grow? — it does, because per-Session overhead competes
with delivery). Keep S fixed within B; vary S across separate B runs.

That is the entire minimum matrix: **4 sweeps + the B/C comparison + a Sessions×GPS
interaction** ≈ 30–40 cells, each isolating a ceiling — versus ~1000 for the grid.

---

## 7. Hold constant vs. vary

- **Vary:** Sessions (primary), GPS, FramesPerGroup, FrameSize — one at a time per
  probe (§6).
- **Hold constant:** the predicate (§3); `T ≥ 30 s`; **P = 1 publisher** (ingest is
  not the throughput ceiling — multi-publisher is a separate study); **1 Track per
  Session**; the **default build** (the `instrument` build is attribution-only);
  **single-Sessions-per-process** (avoids teardown contamination); **hardware**
  (reported on every result).
- **Measure in steady state** (discard the connection ramp); **≥3 runs/cell**,
  report variance.

---

## 8. Fair comparison against other systems (WebRTC SFU, CDN, other MoQ)

Each architecture is bound by a *different* resource. To compare fairly:

1. **Identical offered-load contract** — P publishers, S subscriber Sessions, each
   subscribing to one Track; sweep GPS/F/size identically. Each system fans out on
   its **native transport**.
2. **Identical predicate & hardware.**
3. **Transport-agnostic outputs only** — GPS, aggregate PPS, bandwidth, Sessions,
   delivery ratio, p99, CPU/core, RSS. Never native units.
4. **Compare frontiers + efficiency**, not points — plot max-sustainable-GPS vs
   Sessions (and PPS vs Sessions) for each system on the same axes, plus
   delivered/GPS-per-core. The *shape* — which axis binds, how efficiency degrades
   with Sessions — **is** the architectural comparison.
5. **No-relay baseline** — each system also compared to its transport's raw ceiling
   (qumo vs `BenchmarkPureQUIC_PPS`); the relay's overhead over raw transport is the
   architecture tax.

---

## 9. Result presentation

- **GPS capacity frontier** — x = Sessions (log), y = max sustainable GPS/session
  (log), one curve per FramesPerGroup. Mark target Session counts on x.
- **Four ceilings card** — `Sessions_max`, `GPS_max`, `PPS_max`, `BW_max`, each
  labeled with its binding resource.
- **B-vs-C diagnostic** — delivered PPS at matched offered PPS via high-GPS vs
  high-F: the gap quantifies the churn tax.
- **Efficiency curves** — delivered GPS/core, goroutines/Session, RSS/Session vs
  Sessions (flat = good, super-linear = architectural problem).
- **Comparison overlay** — multiple systems + pure-QUIC baseline on one frontier.

---

## 10. Optimization policy

Every optimization is evaluated on this benchmark. Do **not** optimize for one
workload. For each change, report **which axis it improves** (Sessions / GPS / PPS /
Bandwidth / Memory / Scheduler / churn-tolerance) and **explicitly report
trade-offs** if it helps one axis while hurting another (e.g. raising
`MaxIncomingUniStreams` helps GPS delivery but trades memory/GC). The framework
**discovers** bottlenecks; it does not merely validate a favored fix.

---

## 11. How to run (single-Sessions-per-process)

```sh
# Probe A1 — Sessions ESTABLISHMENT ceiling (burst, all-at-once dial)
for S in 1000 5000 10000; do
  SESSIONS=$S BENCH_DURATION=30s \
    go test -tags=integration -run='^$' \
      -bench=BenchmarkRelay_ConnectionCarry -benchtime=1x ./internal/relay/
done

# Probe A2 — Sessions HOLD ceiling (controlled ramp; BENCH_DURATION must exceed
# ramp time = SESSIONS/RAMP_SESSIONS_PER_SEC + hold). Establishments ≠ hold.
SESSIONS=10000 RAMP_SESSIONS_PER_SEC=100 BENCH_DURATION=130s \
  go test -tags=integration -run='^$' \
    -bench=BenchmarkRelay_ConnectionCarry -benchtime=1x ./internal/relay/

# Probe B — GPS (churn) ceiling at S=256
for G in 0.5 1 2 5 10 20 50 100; do
  SESSIONS=256 GPS=$G FRAMES_PER_GROUP=1 FRAME_SIZE=64 BENCH_DURATION=30s \
    go test -tags=integration -run='^$' \
      -bench=BenchmarkRelay_CapacityFrontier -benchtime=1x ./internal/relay/
done

# Probe C — PPS (socket) ceiling at S=256 (low churn): vary F at GPS=1
for F in 1 2 5 10 30 100; do
  SESSIONS=256 GPS=1 FRAMES_PER_GROUP=$F FRAME_SIZE=64 BENCH_DURATION=30s \
    go test -tags=integration -run='^$' \
      -bench=BenchmarkRelay_CapacityFrontier -benchtime=1x ./internal/relay/
done

# Probe D — Bandwidth ceiling: vary FrameSize at low GPS
for B in 64 1200 16384; do
  SESSIONS=256 GPS=1 FRAMES_PER_GROUP=10 FRAME_SIZE=$B BENCH_DURATION=30s \
    go test -tags=integration -run='^$' \
      -bench=BenchmarkRelay_CapacityFrontier -benchtime=1x ./internal/relay/
done
```

---

## 12. Scientific discipline + WSL2 caveat

- **Do not assume the bottleneck.** When measurements contradict a prior
  conclusion, **update the model**, don't defend the assumption. (This is how GPS
  was promoted to a first-order axis.)
- **WSL2 is a loaded VM** (±10× swing, no GSO verification). On WSL2 the
  methodology is valid for **shape**, **which axis binds**, and the **Sessions
  ceiling** — not for absolute `GPS_max`/`PPS_max`/`BW_max`. Rerun the identical
  probes on bare-metal Linux for absolute ceilings. Stamp the environment on every
  result; ≥3 runs/cell; compare to the pure-QUIC baseline measured the same session.
