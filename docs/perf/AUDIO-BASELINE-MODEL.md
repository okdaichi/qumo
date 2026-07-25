# qumo relay — audio-baseline performance model (single relay)

**Workload:** paced real-time publishing, **30 groups/s, 1 frame/group** (~33 ms
interval), audio-style fan-out. **Not** video GOP grouping. Goal: establish the
current implementation's scaling behavior and limits — re-measured against
current code, prior conclusions not assumed.

**Headline:** the single relay scales **~linearly to ~1000 subscribers**
(p99 ≤ ~56 ms, <2 % loss), then hits a **sharp knee between 1000 and 1500** where
loss explodes (2 % → 46 %) and throughput halves. The knee is **not** relay CPU,
GC, or lock contention — it is **Go-scheduler run-queue contention** from the
per-frame broadcast thundering-herd (~40 % of knee CPU) plus per-connection
quic-go handling (~30 %), on a shared core budget. The isolated relay never
saturates its cores (≤2.75 of 4).

Labels: **[FACT]** measured here vs current code · **[SPEC/EXT]** external/assumed
· **[HYP]** interpretation.

---

## 1. Existing performance assets (inventory)

**In-process microbenchmarks** (`internal/relay/*_test.go`, `-tags integration`):
- **`BenchmarkRelayChain_FanoutSingleRelay`** (`single_relay_bench_test.go`) —
  **the** single-relay fan-out capacity/latency bench (publisher → 1 relay → K
  subscribers, all in-process). Env: `FANOUT_KS`, `FANOUT_GAP` (pacing),
  `FANOUT_FPG` (frames/group), `BENCH_DURATION`. **Used as the baseline here.**
  Valid; the one caveat is that relay + subscribers + publisher share the process
  (co-located load — see §4).
- `BenchmarkRelayChain_FanoutSweep{,_Load,_ObjSize}` — 2-hop origin→leaf→sub.
- `BenchmarkPureQUIC_PPS` — pure quic-go socket PPS ceiling (~222 K on Linux).
- Component micro: `GroupCache_*`, `GroupRing_*`, `TrackDistributor_Broadcast`,
  `BroadcastNotify_*`, `FramePool*`, `EgressAccounting_Fanout*`, `ProcessGroup`.
  These isolate data-structure costs; all valid, all show relay code is cheap.

**Out-of-process load generator** (`internal/loadgen`, `qumo loadgen
{publish,subscribe,carry}`): rate-paced publisher (`gps`), subscriber with
capacity + e2e latency histogram (latency on the unmerged #350 branch). Drives
relay-isolated tests.

**Nomad harness** (`bench-nomad/`): `run-study.sh` + `study.py` +
`gen-cluster.py` — multi-topology (hub + K edges) `raw_exec` cluster, relay
pinned to cores 0–3, loadgen to 4–7, scrapes each relay's `/metrics`
(`process_cpu_seconds_total`, RSS, `go_gc_*`, `go_memstats_mallocs_total`). This
is the **relay-isolated** capacity/resource tool. Data: `study-all.jsonl`.

**Instrumentation** (`-tags instrument`, no-op default): stage-latency (A ingress
/ R ring-wait / O group-open / C write-frame) plus mechanism split (R.fill /
R.wake, behind/woken, concurrency gauges, broadcast dur, fillSem wait, group
inter-arrival). Server exposes it via `Server.StageLatency()`; the bench logs it.
`RELAY_PPROF=1` exposes `/debug/pprof/*` on a live relay.

**Reports** (`docs/perf/`): committed `CAPACITY-REPORT.md`,
`OPTIMIZATION-LEDGER.md`; this session (untracked/Gist) `LATENCY-ATTRIBUTION`,
`FANOUT-MECHANISM`, `HIERARCHY-REPORT`, `SLO-CAPACITY-STUDY`,
`WORKLOAD-MODEL-REVIEW`, `PUBLISHER-CADENCE-CHECK`. Dashboard:
`dashboard-results/index.html`.

**No new benchmark was created** — the existing `FanoutSingleRelay` bench (with
the `FANOUT_GAP`/`FANOUT_FPG` params and the instrument stage report added this
session) covers the baseline.

## 2. Baseline scaling curve (measured, current code)

**In-process** (`FanoutSingleRelay`, 8 shared cores, gap=33 ms, 1200 B frames,
20 s, 5 s settle). Aggregate OpenGroupAt/s = WriteFrame/s (1 frame/group).

| Subs | e2e p50 | e2e p99 | loss % | fps (of 30) | OpenGroupAt=WriteFrame /s | R.wake p50 | heap MB |
|---|---|---|---|---|---|---|---|
| 100 | 0.9 ms | 1.8 ms | 0 | 29.7 | ~3.0 K | 0.44 ms | 6 |
| 500 | 5.1 ms | 16.7 ms | ~0 | 29.9 | ~14.9 K | 1.20 ms | 26 |
| **1000** | 12.0 ms | 56 ms | 2.0 | 28.8 | ~28.8 K | 3.32 ms | 61 |
| **1500** | 30.5 ms | 304 ms | **46** | **16.0** | ~24 K | 9.9 ms | 62 |
| 2000 | 28.6 ms | 167 ms | **50** | **14.9** | ~30 K | 8.4 ms | 73 |
| 5000 | 222 ms | 1133 ms | **82** | **5.3** | ~26 K | 44.6 ms | 274 |

**Isolated relay** (Nomad K=0, relay pinned to 4 cores, loadgen separate — from
`study-all.jsonl`; **pre-#348**, so current code is marginally better):

| Subs | conn | loss % | p99 | **relay CPU (of 4)** | RSS MB | goros | allocs/s | egress Mbps | GC max |
|---|---|---|---|---|---|---|---|---|---|
| 500 | 500 | 0 | 31 ms | **1.00** | 399 | 57 | 334 K | 123 | 0.24 ms |
| 1000 | 992 | 0.2 | 227 ms | **1.88** | 981 | 57 | 656 K | 271 | 0.77 ms |
| 1500 | 1494 | 7.3 | 737 ms | **2.47** | 1494 | 1646 | 910 K | 375 | 0.77 ms |
| 2000 | 1978 | 16 | ≥1000 ms | **2.75** | 2421 | 1541 | 1.04 M | 416 | 1.6 ms |

Per-session cost (both setups agree): **~7 goroutines, ~470 KB RSS** — dominated
by quic-go per-connection state; relay/gomoqt code is <1 % of it.

*Frame size note:* 1200 B (the established baseline) is heavier than real audio
(~160 B Opus). Real audio frames would lower egress bytes and shift the ceiling
up; 1200 B is kept for comparability. **[HYP]**

## 3. Scaling analysis — linear then a sharp knee

**[FACT] Linear regime to ~1000 subscribers.** Latency grows ~linearly with N:
R.wake p50 = 0.44 → 1.20 → 3.32 ms at N = 100 → 500 → 1000 (≈ N/300 µs), loss
< 2 %, full 30 fps. Isolated relay CPU grows linearly too (1.0 → 1.9 cores,
2 cores/1000 subs). **This is class-A linear scaling in the healthy range.**

**[FACT] Sharp non-linear knee between 1000 and 1500.** In-process, loss jumps
2 % → 46 % and fps halves (28.8 → 16) crossing 1000→1500; by 5000 it is 82 % loss.
Isolated, loss crosses 1 % between 1000 (0.2 %) and 1500 (7.3 %). **The knee is at
~1000–1200 subscribers for this workload/environment.**

**[FACT] The knee is not the relay running out of CPU.** The isolated relay uses
only **2.75 of 4 cores at N=2000** — it never saturates, yet loss climbs to 16 %.
So the ceiling is *external to the relay's compute* (co-located load generation +
the shared-core scheduler cost of fan-out), not relay throughput.

## 4. What consumes the time at the knee (profiled, N=1500 in-process)

CPU profile (84 s samples / 25 s ≈ 3.3 cores busy), decomposed:

| Cost | Share (cum) | What it is |
|---|---|---|
| **Go runtime scheduler/sync** | **~40 %** | `futex` 12 % + `selectgo` 17 % + `lock2`/`unlock2`/`sellock`/`gopark` ~20 % (overlapping) — waking/parking the goroutine herd |
| **quic-go `Conn.run`** | **30 %** | per-connection event loops for 1500+ connections (relay-serving **and** co-located subscriber clients) |
| **UDP `sendmsg`** | 13 % | egress syscalls |
| relay egress code (`deliverGroup`) | 15 % (11 %) | the relay's own fan-out delivery |
| GC (`scanObjectsSmall`) | ~2 % | **not** a factor |
| **relay lock contention** | **~0 %** | mutex profile is **99 % `testing.Helper`** (harness); relay `groupRing.reserve` = 0.08 % |

**[FACT] Re-confirmed against current code — the answer is scheduler contention,
not the previously-suspected candidates.** GC is 2 %, relay locks are nil (the
mutex profile is the in-process test harness, not the relay). The dominant cost is
the Go scheduler moving thousands of goroutines on and off a fixed core budget,
plus quic-go's per-connection machinery.

## 5. Fan-out execution model — validated (Case B)

The R stage (group reserve → egress pickup) is split at the first-broadcast
instant:

**[FACT] R.fill (reserve → broadcast) is flat ~6–8 µs at every N (100 → 5000).**
Group data is ready almost instantly; fill is never the queue. `fillSem wait` is
~1 µs throughout → no ingest backpressure.

**[FACT] R.wake (broadcast → pickup) is the entire R and scales with N**
(0.44 → 1.2 → 3.3 → 9.9 → 44.6 ms). The delay is subscribers being woken and
scheduled, not group availability.

**[FACT] The wakeup is the bottleneck.** The trace scheduler-latency profile at
N=1000 attributes **51 % of all runnable-but-not-running time to
`broadcastNotify.notify`** — `broadcast()` (once per frame) makes all N egress
goroutines runnable at once; they queue for the cores. `broadcast dur` itself
grows with N (19 µs → 585 µs → 1.6 ms at N = 100 → 1500 → 5000) as closing the
notify channel wakes more waiters.

**[FACT] Groups are parallel, not serialized.** `R.fill` flat + up to hundreds of
concurrent `deliverGroup`s + `fillSem` idle ⇒ no group/frame serialization; the
latency is shared-execution-resource (scheduler) pressure. This is **Case B**, and
it reproduces on current code exactly as in `FANOUT-MECHANISM.md`.

**[HYP] Why the knee is where it is.** Each per-frame broadcast wakes N goroutines;
median wake latency ≈ N × per-pickup-work ÷ cores. Below ~1000 the herd drains
within the 33 ms frame budget; past it, wake latency approaches the frame interval,
groups back up, the ring (size 8) evicts, and loss explodes — a positive-feedback
collapse. More cores or fewer subscribers-per-relay (hierarchy) push the knee out;
this is exactly why hub→edge cut p99 ~7× in the hierarchy study.

## Conclusions

- **[FACT]** Current single relay, audio baseline: **~1000 concurrent subscribers
  at p99 ≤ ~56 ms and <2 % loss**; a sharp knee at ~1000–1500; total collapse by
  ~2000 (this environment).
- **[FACT]** The limit is **not** relay CPU (≤2.75/4 cores isolated), GC (2 %), or
  relay locks (nil). It is **Go-scheduler run-queue contention from the per-frame
  broadcast herd** (~40 % of CPU) plus per-connection quic-go handling, on a shared
  core budget — **Case B, re-confirmed against current code.**
- **[FACT]** Scaling is **linear to the knee, then a positive-feedback collapse**
  (not graceful degradation).

## Environment limits & uncertainty
- Single-host WSL2, 8 cores. In-process bench co-locates relay + subscribers +
  publisher → the ~1000–1500 knee is partly the **combined** load saturating 8
  cores, not the relay alone (the isolated relay reaches only 2.75/4 cores). The
  **true relay-only subscriber ceiling is not established here** — it needs
  distributed load (separate hosts), the standing #342 gap.
- Isolated-relay resource table is **pre-#348** (current code marginally better on
  deliverGroup CPU; latency unchanged).
- 1200 B frames > real audio (~160 B) → capacity numbers are conservative for true
  audio.
- Absolute latencies are environment-specific (core count, pinning, loopback);
  the **shape** (linear→knee) and the **mechanism** (Case B) are the portable
  results.

### Repro
`FANOUT_KS=<list> FANOUT_GAP=33ms FANOUT_FPG=1 BENCH_DURATION=20s
./relay_mech.test -test.bench=…FanoutSingleRelay$ -test.benchtime=1x
[-test.cpuprofile=… -test.mutexprofile=… -test.trace=…]` (integration,instrument,
Linux). Isolated relay: `bench-nomad/run-study.sh` with `KLIST=0`. Scheduler
latency: `go tool trace -pprof=sched trace.out | go tool pprof`.
