# Single-relay tail-latency attribution at high fan-out (N≈1000)

**Date:** 2026-07-24 · **Environment:** WSL2, 8 cores (i7-10700K class), in-process
integration bench (`BenchmarkRelayChain_FanoutSingleRelay`), publisher 30 fps × 1200 B,
1-frame groups, 20 s runs, first 5 s (ramp) excluded from all percentiles.

**Question:** why does single-relay p99 e2e latency reach ~227 ms at N=1000 (Nomad study),
while a hub+8-edge hierarchy at the same N sits at ~34 ms?

## Method

Per-stage instrumentation behind a new `//go:build instrument` tag (zero-overhead no-op in
the default build; `time.Now` lives inside the tagged methods). Stages, per frame/group:

| Stage | Meaning | Stamp boundary |
|---|---|---|
| A ingress | per-frame clone + ring publish | around `groupCache.append` in `groupRing.fill` |
| R ring-wait | group arrival → egress pickup, per subscriber | `processGroup` reserve → `deliverGroup` entry |
| O group-open | `OpenGroupAt` (QUIC uni-stream open) | inside `deliverGroup` |
| C write-frame | `WriteFrame` service | inside `deliverGroup` |
| E2E | publisher stamp → subscriber read | payload[8:16] UnixNano (existing) |

Residual = E2E − (A+R+O+C) ≈ upstream leg + quic-go send-path drain + subscriber read
scheduling. Instrumented build used for **attribution only**; e2e/throughput numbers below
come from the clean build (3 runs).

## Measured results (fact)

Clean build baseline:

| K | e2e med | e2e p99 |
|---|---|---|
| 100 | 1.2 ms | 2.8 ms |
| 1000 (×3) | 11.6–12.4 ms | 64 / 82 / 96 ms |

(In-process med at K=1000 matches the Nomad K=0 study's p50 of 11.3 ms; the Nomad p99 of
227 ms is worse than in-process because there the relay was pinned to 4 cores and shared
the host with an out-of-process load generator.)

Instrument build, steady state (two K=1000 runs agreed):

| Stage | K=100 p50 / p99 | K=1000 p50 / p99 | K=1000 max |
|---|---|---|---|
| A ingress | 1 µs / 4 µs | 2 µs / 7 µs | 20 µs |
| **R ring-wait** | **476 µs / 1.6 ms** | **3.8 ms / 20–34 ms** | 130–180 ms |
| O group-open | 6 µs / 35 µs | 8 µs / ~40 µs | 130–180 ms |
| C write-frame | 1 µs / 2 µs | 1 µs / 2 µs | 4.8–8.2 ms |
| E2E (same run) | 0.9 ms med | 12.3 ms med, 72–100 ms p99 | — |

Profiles (clean build, K=1000, 25 s window): total CPU 83.3 s ≈ 3.24 cores avg (not
core-saturated on average). `Conn.run` 36 % cum, sendQueue→`sendmsg` syscall 18 %,
receive path 14 %, raw `Syscall6` flat 19 %. Relay egress (`ServeTrack`) 12 % cum, and
inside `deliverGroup` itself: **61 % `OpenGroupAt`, 27 % `context.WithTimeout` timer
setup**, `WriteFrame` not visible. Mutex profile shows **no relay-side contention** (the
one large entry is the `testing` package's mutex during the dial ramp, excluded by
settle); block profile is ordinary idle waits.

## Attribution (measured decomposition)

At K=1000, of the 12.3 ms median e2e:

- **~31 % is R (3.8 ms): relay-internal queueing** — but not a lock or a slow data
  structure. One `broadcast()` per frame wakes all 1000 egress goroutines at once; each
  pickup costs ~15 µs CPU in `deliverGroup` (measured: 6.26 s / 415 K deliveries) plus
  quic-go enqueue work. The herd drains through 8 cores, so the median subscriber waits
  ≈ K × per-pickup-cost / cores ≈ 1000 × ~30 µs / 8 ≈ 3.8 ms — which is exactly the
  measured R p50. R is a **service-rate limit** (fixed per-subscriber per-group cost ×
  K ÷ cores), not contention.
- **~69 % is the residual (~8.5 ms): downstream transport drain** — quic-go per-connection
  event loops, packet packing, `sendmsg` for 1000 connections (~30 K pps), and (in this
  in-process bench) subscriber-side read scheduling on the same cores.
- A, O, C are all microseconds and flat in K → the qumo fan-out data structures
  (lock-free ring, notify, WriteFrame path) are **not** the bottleneck.

The tail (p99) is the same mechanism amplified: R p99 20–34 ms is the unlucky-position
subscriber in the herd plus scheduler jitter; occasional O max ≈ R max spikes (130–180 ms)
show rare stream-open stalls under burst.

This also explains the hierarchy result mechanistically: hub+8 edges cuts K-per-relay to
125, shrinking both the herd drain (R ∝ K) and each socket's send-queue drain — matching
the measured 227→34 ms.

## Optimization candidates (ranked, with evidence)

1. **Reduce per-group per-subscriber constant cost — drop the per-delivery
   `context.WithTimeout`** (handler.go `deliverGroup`). 27 % of deliverGroup CPU is timer
   construction/teardown, 415 K times per 25 s at K=1000. Replace with a reusable timer or
   a deadline check only on the blocking path. Expected: ~4 µs less per delivery → ~13 %
   lower R p50 at K=1000 (~0.5 ms), more at higher K. Risk: low; must preserve the
   MAX_STREAMS backpressure timeout semantics.
2. **Amortize stream churn: larger groups.** 61 % of deliverGroup CPU is `OpenGroupAt`,
   plus per-stream packets/state downstream — paid per group, and this workload is 1
   frame/group at 30 groups/s/subscriber (30 K stream opens/s total). GOP-sized groups
   (e.g. 1–2 s) cut that by 30–60×. This is a content/packaging decision (and consistent
   with the earlier finding that the "K≈128 ceiling" was a group-churn artifact), not a
   relay code change.
3. **Syscall batching (GSO)** — 19 % of CPU is UDP syscalls; known lever from the
   throughput study (Linux ~222 K pps socket ceiling). Verify quic-go's GSO path is active
   in the target environment before expecting gains.
4. **Hierarchy** — already measured: 7× p99 at N=1000. The shipped answer for latency at
   fan-out on a single host.

**Refuted / not worth pursuing (measured):** ingest path (A: µs, flat), WriteFrame/egress
service (C: 1–2 µs, flat), notify mechanism (O(1), no contention in mutex profile),
lock contention anywhere in the relay (mutex profile clean), GC (prior study: ≤12 ms
pause, ~0.1/s, GOGC=800).

## Caveats

- In-process bench: subscriber CPU shares the relay's cores; the residual therefore
  overstates what a relay-only host would see. Relative stage comparison and the R
  mechanism are the valid results; absolute numbers are environment-specific.
- Loss% printed by the instrumented runs (~28 %) is a settle-filter artifact (first 5 s of
  20 s discarded), not real loss (healthy K=100 shows the same ~25 %).
- Instrumented builds perturb throughput; all e2e claims come from the clean build.
- p99 at K=1000 has ~±17 % run-to-run spread; conclusions rest on the stable medians and
  the stage decomposition, which reproduced across runs.

## Optimization cycle: candidate #1 (reusable OpenGroupAt deadline)

Pre-registered: replacing per-delivery `context.WithTimeout` with a reusable
timer/context would cut R p50 ≥10% at K=1000. **REFUTED as a latency win** —
measured after (2 instrument + 3 clean runs): R p50 3.72/3.78 ms (−1.5%), e2e
med 12.1 ms (unchanged), p99 within noise. The eliminated work was real but not
on the herd's critical drain path.

Re-adjudicated as a **CPU/alloc efficiency win (CONFIRMED)**:

- Microbenchmark (`BenchmarkOpenDeadline`, 10 runs, benchstat): 354.8 ns ± 2% →
  57.8 ns ± 4% per delivery (6.1×); 272 B / 4 allocs → **0 / 0**.
- System level at K=1000: `deliverGroup` CPU 6.26 s → 4.25 s (−32%);
  whole-process profile samples 83.3 s → 72.5 s (−13%, single run each — treat
  as corroboration, not a headline). At 30 K deliveries/s this removes ~120 K
  allocs/s of timer garbage.
- No regression on any measured metric; full test suite + `-race` green
  (default and instrument builds).

Kept and shipped as an efficiency change (`internal/relay/open_timeout.go`),
explicitly **not** claimed as a latency improvement. Lesson recorded: at this
fan-out the herd drain is bounded by quic-go enqueue + scheduler work, so
shaving off-path CPU inside deliverGroup does not move R.

## Artifacts

- Instrumentation: `internal/relay/stage_latency{,_noop,_instrument}.go` + stamps in
  `handler.go` / `group_cache.go` / `stats.go` (default build: no-op, all tests green;
  `-tags instrument` tests green).
- Harness: `bench-lat/` (untracked) — run scripts + `cpu/block/mutex_k1000.pprof`.
