# High-fan-out latency mechanism: serialization (A) vs shared-resource contention (B)

**Question:** does fan-out processing of one Group block subsequent Groups (A —
serialization), or do Groups proceed in parallel but suffer shared-execution-
resource pressure that delays egress pickup (B)?

# Finding

## Case B confirmed — group parallelism is preserved; the R stage is egress wake + scheduler-run-queue latency, triggered by the broadcast thundering herd.

Groups are **not** serialized. Group data becomes available almost instantly at
every fan-out; the entire R-stage latency is the gap between a group being
broadcast and a subscriber's egress goroutine being **scheduled onto a CPU** to
pick it up. The synchronization/execution boundary is `broadcast()` making all N
egress goroutines runnable simultaneously, after which they queue for a fixed set
of cores.

No optimization was performed. Instrumentation only (behind `-tags instrument`,
zero-overhead default build).

---

## Environment

WSL2, 8 cores, relay pinned to 4 (0–3), load generator on 4–7. Publisher 30 fps ×
1200 B, **1 frame/group**. `BenchmarkRelayChain_FanoutSingleRelay`, 20 s run,
first 5 s excluded. Instrument build. N ∈ {100, 1000, 1500}.

## Instrumentation added (temporary, `instrument` tag)

R (reserve → egress pickup) is split at the instant `fill` first broadcast the
group, plus a behind/woken classification and overlap gauges:

| Instrument | What it isolates |
|---|---|
| **R.fill** = reserve → first broadcast | fill-worker / ingest latency (A on the ingest side) |
| **R.wake** = first broadcast → pickup | egress wake + schedule latency (B) |
| **R.behind** / **R.woken** | pickup reached directly (subscriber behind) vs after a notify wait (caught up) |
| **deliverSpan** = deliverGroup entry → end | how long a subscriber is busy per group |
| **fillSem wait** | ingest backpressure (fill-slot starvation) |
| **broadcast dur** | is broadcast() blocking? |
| **maxConcurrentDeliveries** | egress goroutines inside deliverGroup at once (parallelism) |

Plus block/mutex profiles and a runtime execution trace at N=1000.

## Measured facts

### 1. R is ~100% wake latency, ~0% fill latency — at every fan-out

| N | R p50 | **R.fill p50** (reserve→bcast) | **R.wake p50** (bcast→pickup) | R p99 |
|---|---|---|---|---|
| 100 | 502 µs | **9 µs** | **490 µs** | 1.37 ms |
| 1000 | 7.06 ms | **11 µs** | **7.05 ms** | 37.6 ms |
| 1500 | 6.71 ms | **8 µs** | **6.69 ms** | 42.7 ms |

**R.fill stays flat at ~8–11 µs regardless of fan-out.** Group data is ready
almost instantly; the group is never waiting on the fill path. The entire growth
of R (0.5 ms → 7 ms as N goes 100 → 1000) is R.wake. → **ingest/fill
serialization is ruled out.**

### 2. No ingest backpressure

`fillSem wait` p50 = **1 µs** at N=100/1000/1500 (max ≤ 14 µs). The fill worker
pool never stalls the ingest goroutine, so group *intake* is never serialized.

### 3. Subscribers are caught up and waiting — not behind

At N=1000, of 425 000 pickups: **R.woken n=420 321 (98.9%)**, R.behind n=4 679
(1.1%). Subscribers overwhelmingly reach the next group **after a notify wait**
(they were idle, caught up), not by draining a backlog. `deliverSpan` p50 =
**10 µs** — a subscriber's own delivery is instant. → **per-subscriber
self-serialization is not the mechanism** for the bulk of the latency (the 1.1%
"behind" tail carries the worst p99, but it is a small minority).

### 4. Groups are delivered massively in parallel

`maxConcurrentDeliveries` = **10 (N=100) → 342 (N=1000) → 1498 (N=1500)** — it
scales directly with fan-out. Hundreds-to-thousands of egress goroutines are
inside `deliverGroup` simultaneously. Groups overlap heavily; they are **not**
processed one-at-a-time.

### 5. The delay is scheduler run-queue latency, caused by the broadcast herd

The execution trace's **scheduler-latency profile** (time goroutines spend
runnable but not running) at N=1000:

| Function | share of all scheduler-latency |
|---|---|
| **`(*broadcastNotify).notify`** | **51.2%** (4083 s of 7983 s) |
| `runtime.selectnbsend` | 15.4% |
| everything else | < 2% each |

`broadcast()` closes the notification channel, which makes **all ~1000 waiting
egress goroutines runnable in the same instant**; they then queue for the 4
pinned cores. The profiler attributes over half of all "waiting for a CPU" time
to that single wakeup point. This is a textbook thundering-herd → run-queue
contention pattern.

### 6. No lock contention; broadcast doesn't block on delivery

Mutex profile: runtime-internal locks only (`runtime.unlock` 93%), no
application-level contention (consistent with PR #348). `broadcast dur` p50 =
182 µs at N=1000 — it returns promptly (does **not** wait for subscriber
processing), confirming the notify is non-blocking in the "wait for work" sense.

## Interpretation (supported by, but going beyond, the facts)

- The R-stage growth with fan-out is **not** later groups being blocked by earlier
  fan-out work. Each group is filled and broadcast in µs, and delivered in
  parallel by all subscribers. The cost is that a broadcast wakes N goroutines at
  once, and with a fixed core budget the scheduler can only run them a few at a
  time — so the median subscriber waits ≈ (goroutines ahead of it in the run
  queue) × (per-pickup CPU) before it even starts its (10 µs) delivery. This is
  exactly the service-rate model PR #348 inferred (R ≈ N × per-pickup ÷ cores),
  now **directly confirmed** as scheduler run-queue latency rather than inferred
  from throughput.
- `broadcast()` itself getting more expensive with N (14 µs → 182 µs from N=100 →
  1000) is a secondary effect: closing a channel with more waiters costs more
  runtime work, and it is on the fill goroutine's path. It is not the dominant
  term (R.wake ≫ broadcast dur), but it is real.
- Because the boundary is CPU scheduling of a simultaneous wake, **more cores or
  fewer goroutines-per-core is the structural lever** — which is exactly why the
  hub→edge hierarchy (N/K subscribers per relay) cut p99 ~7×: it divides the herd.

## Remaining uncertainty

- **`maxConcurrentGroups` gauge is unreliable** (read 0/0/2). Its
  increment-on-first-pickup / decrement-on-release accounting races with
  group-cache pool reuse and under-counts. The parallelism conclusion rests on
  `maxConcurrentDeliveries` (clean, scales 10→342→1498) and the R.fill/R.wake
  split, not on this gauge. Worth fixing if a precise concurrent-generation count
  is ever needed; not load-bearing here.
- Single-host WSL2 with the relay pinned to 4 cores and co-located with the load
  generator **amplifies** the scheduler contention (fewer cores, shared with the
  generator). The *mechanism* (broadcast → simultaneous wake → run-queue) is
  structural and would persist on more cores, but the absolute R.wake magnitudes
  are environment-specific.
- The 1.1% "behind" pickups carry the worst tail (R.behind p99 = 110 ms at
  N=1000). Their exact trigger (a subscriber that fell behind during a GC pause or
  a scheduling hiccup, then had ≥1 group queued) is not separately instrumented —
  a candidate for follow-up if the p99 (not p50) tail becomes the target.

## Bottom line

**B, decisively.** Groups run in parallel (fill is instant, up to ~1500
concurrent deliveries); the high-fan-out R latency is egress goroutines sitting
**runnable-but-unscheduled** after `broadcast()` wakes the whole herd onto a
fixed core budget. The execution boundary is the simultaneous wakeup, not any
group/frame serialization. No ingest serialization, no per-subscriber
self-blocking (for the bulk), no lock contention.

### Appendix — instrumentation & repro
- Code (uncommitted, `instrument` tag): `internal/relay/stage_latency*.go`,
  stamps in `handler.go` / `group_cache.go`, bench logging in
  `single_relay_bench_test.go`. Default build unchanged (no-op collector).
- Run: `FANOUT_KS=<N> BENCH_DURATION=20s FANOUT_GAP=33ms
  ./relay_mech.test -test.bench=…FanoutSingleRelay$ -test.benchtime=1x
  -test.trace=… -test.blockprofile=… -test.mutexprofile=…` (integration,instrument,
  Linux). Scheduler latency: `go tool trace -pprof=sched trace.out | go tool pprof`.
