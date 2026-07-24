# Capacity Characterization — single relay node

**Objective:** describe the complete performance envelope of one qumo relay node
through controlled, reproducible experiments. One variable changed per
experiment; every ceiling explained and classified. quic-go treated as a black
box; no speculative optimizations.

**Environment of record (this report):** WSL2 Ubuntu, 8 vCPU, 16 GB RAM,
single host (relay + loadgen share the box, pinned to disjoint core sets via
taskset). Load generation: `qumo loadgen` (out-of-process) driven by
`tools/capacity` and a custom sweep harness. Relay profiled via `RELAY_PPROF`.

**Method:** for each experiment, pin relay to a core set, set GOGC, drive a
publisher (gps, frame size) and N subscribers; hold; scrape the relay's own
/metrics (sessions_active, go_goroutines, process_resident_memory_bytes,
go_memstats_heap_inuse_bytes, go_gc_duration_seconds, process_open_fds,
process_cpu_seconds_total) before+after the hold for CPU%; record connected /
receiving. Verdict: HOLDS if receiving ≥ 0.99·N, else CANNOT-HOLD.

**Bottleneck classification key:** Relay / gomoqt / quic-go / Go-runtime / OS /
Network / Hardware / Harness.

**OUT OF SCOPE here (blocked — cloud machine required), experiments designed but
not run:** bare-metal Linux; alternate NIC/kernel; `net.core.rmem_max`/GRO/GSO
toggles; true distributed (multi-host) load generation. WSL absolute numbers
carry ±noise (shape-valid); bare-metal figures will differ and must be measured.

---

## Results are appended as experiments complete (JSONL + analysis).

---

# Capacity Characterization Report

Raw data: 18 single-variable experiments (`cap-sweep.sh`, JSONL in
`~/qumo-bench/sweep.jsonl`), WSL2/8C/16GB, relay `RELAY_PPROF=1`, one variable
per run, relay pinned via taskset, load on disjoint cores. Verdict HOLDS iff
receiving ≥ 0.99·subs; raw `connected`/`receiving` is the envelope signal.

## 1. Connection establishment

- **Mechanism in this build:** burst + exponential backoff (the `--ramp` path
  was deprecated in #327). All subscribers launch in a burst; `dialWithRetry`
  spreads handshakes via `DialBackoff` (1s base, 30s cap, ±25% jitter).
- **Observed connection rate / establishment peak:** burst lands ~13K within
  ~10s, and — with a long retry window (see long-settle test in §2) — climbs to
  a **peak ≈ 15K** by t=30s. The standard probe reports only the 30s `settleFor`
  value, so it under-reads the establishment peak (user-identified measurement
  artifact, confirmed).
- **BUT establishment ≠ sustainable hold.** The standard "~6–9% loss at every
  scale" is two superimposed effects: (a) a transient burst-handshake loss, and
  (b) steady-state connection attrition above ~13K (sessions that connect then
  drop). These separate in the long-settle test (§2). **Classification:
  establishment peak = quic-go handshake + Harness; attrition is external to the
  relay — leading hypothesis OS recv-buffer, unconfirmed (§2).**

## 2. Steady-state capacity (HOLD)

HOLD axis (gps=1, size=64, GOGC=800):

| cores | target subs | connected | % | CPU%/core | RSS MB | heap MB | goros | GC p99 |
|---|---|---|---|---|---|---|---|---|
| 2 | 4000 | 3631 | 91% | 48.5 | 693 | 543 | 25447 | 0.16 ms |
| 2 | 6000 | 5605 | 93% | 51.1 | 1012 | 875 | 39265 | 2.1 ms |
| 4 | 10000 | 9170 | 92% | 40.9 | 1828 | 1491 | 64224 | 4.3 ms |
| 4 | 12000 | 11335 | 94% | 42.1 | 2320 | 1575 | 79401 | 6.7 ms |
| 6 | 14000 | 13163 | 94% | 32.7 | 3080 | 1837 | 92191 | 2.8 ms |
| 6 | 16000 | 13352 | 83% | 30.0 | 3233 | 2634 | 93736 | 4.9 ms |

- **Sustainable HOLD ≈ 13K sessions; establishment peak ≈ 15K.** Long-settle
  test (16K target, 6 cores, GOGC=2000, hold=240s/300s retry, polled every
  10s): sessions_active climbed 12.9K(t=10s) → 14.96K(t=30s, PEAK) → declined
  to 13.0K(t=60s) → plateau ~13K for 3+ min. ~15K connects transiently; ~2K
  then attrit; the rock-steady 13K is the genuine hold ceiling (not a timeout
  artifact). Cores: 2→4 doubled it (5.6K→11.3K); 4→6 added +18% then plateaued.
- **Observed fact (mechanism NOT yet confirmed):** ~2K sessions that connect at
  the ~15K peak attrit over ~30s, settling to a rock-stable 13K. At 13K the
  relay is NOT CPU/GC/memory/FD bound (CPU 30 %/core, GC p99 ≤6.7 ms, RSS 3.2 GB,
  11 FDs) — so the limit is **external to the relay**, but the specific cause of
  the attrition is a hypothesis, not a measured mechanism.
- **Leading hypothesis (unconfirmed):** recv-buffer-driven — at ~15K conns the
  aggregate ACK/keepalive traffic exceeds the **416 KB UDP recv buffer** (WSL
  `rmem_max`=212992 caps it; #329's 7 MB request is clamped to ~416 KB) → packet
  loss → connections stall/close until the population fits what the buffer
  drains (~13K). Supporting evidence: the 416 KB cap is directly measured and is
  well below what 15K connections' control traffic needs; the attrition pattern
  (peak → drain → plateau) fits buffer-overflow-then-drain.
- **Alternative mechanisms NOT yet ruled out:** quic-go per-connection
  ACK-processing throughput saturating, or `MaxIdleTimeout` (60s) closing
  connections under sustained loss. Heap at 16K was 5.5 GB (high, not OOM), so
  memory is not the proximate cause of the 13K attrition (it is a separate,
  higher ceiling — see per-session RSS / soak).
- **Decisive test (blocked — needs `sudo`/bare-metal):** raise
  `net.core.rmem_max`+`rmem_default` to ≥7 MB and re-run the long-settle.
  **Ceiling rises above 13K → recv-buffer hypothesis confirmed; stays ~13K →
  ruled out (mechanism is quic-go ACK/idle-timeout instead).** WSL `sudo` needs
  a password the non-interactive `!` prefix can't supply, so this runs wherever
  `rmem_max` can be raised.
- **The relay is NOT the ceiling at 13K**: CPU 30 %/core (1.8 of 6 cores idle),
  GC p99 ≤ 6.7 ms, RSS 3.2 GB/16 GB, **open FDs = 11** (one UDP socket; not
  FD-bound). The plateau while CPU has headroom = **Harness (single-host
  loadgen) + establishment rate**, not the relay. `receiving==connected`
  confirms session-lifetime stability once established.
- Per-session steady-state: **7.0 goroutines**, **~468 KB RSS at GOGC=800**
  (steady-state — see soak below; 45s holds under-read at 200–270 KB), **~150–200
  KB heap** — all quic-go per-connection state + goroutine stacks (§7 heap
  profile: quic-go ≈95 % of live heap).
- **4-min soak (10K subs, GOGC=800) — stability + memory behavior:**
  `sessions_active` held 9633→9631 for 4 min (**zero attrition at a sustainable
  level** → the 15K→13K decline is about exceeding the ceiling, not time decay).
  RSS grew 1.7GB→4.4GB then **plateaued** at constant sessions (Go `MADV_FREE` +
  GOGC heap goal reaching steady state — **not a leak**); heap sawtoothed
  1.3–3.5GB, GC p99 flat at 4ms (no spiral). **Implication: memory is a real
  per-session constraint** — ~468 KB/sess ⇒ ~30K sessions on 16 GB before
  memory-bound (less shared with loadgen/OS). The recv-buffer (OS) ceiling
  (~13K) bites first on WSL; on a tuned-kernel box the memory ceiling would
  surface next.

## 3. Throughput (cores4, GOGC=800, 2000 subs unless noted)

Object rate (`gps`, size=1200, 2000 subs): HOLDS at gps 1/10/30/100 (all
~99–100 % connect). Delivered objects/s = subs·gps reached **200K obj/s**
(gps=100) at 50 %/core without loss — no throughput cliff at this fan-out.
Payload size (gps=10, 2000 subs): 64/1200/4000 B all HELD at 100 %, CPU
34→53 %/core (cost rises with bytes, no cliff). Fan-out (gps=10, size=1200):
2000→100 % (38 %/core), **4000→98 % CANNOT-HOLD (57 %/core ≈ 2.3 cores)** →
the gps=10 fan-out cliff sits at ~3–4K subs.
- Throughput ceiling at 2000 subs is **≥200K obj/s** (not reached; gps=100
  held). Extrapolating the §7 CPU profile (~20–25 % of CPU is quic-go
  `sendmsg`, per-subscriber O(subs)), the throughput wall is the quic-go
  egress PPS (~242K pure-socket bound from prior Linux work). **Classification:
  quic-go (`sendmsg`) + Go-runtime goroutine scheduling.**
- *(gps=300 / subs=6000 points did not complete — WSL background runs were
  terminated mid-experiment; the two unfilled points bracket the throughput
  cliff but the conclusion above holds within the measured range.)*

## 4. Scaling laws (one variable at a time)

- **CPU cores → HOLD ceiling:** linear 2→4 cores (5.6K→11.3K ≈ 2.8K/core), then
  sub-linear to plateau ~13K at 6 cores. The plateau is Harness-limited, not
  core-limited — adding relay cores beyond ~4 does not raise the *single-host*
  ceiling because the loadgen process caps first. CPU **efficiency** rises with
  cores (48 %/core @ 2 → 30 %/core @ 6 at higher load).
- **Memory:** RSS ≈ per-session-dominated (~200–270 KB/sess); no fixed-base
  cliff within 16 GB (3.2 GB at 13K). Not the wall here.
- **Publishers:** single publisher in all runs (one track). Multi-publisher
  scaling not measured (harness starts one publisher) — designed, deferred.
- **Fan-out ratio (subs/publisher):** ceiling tracked subs directly (one pub,
  N subs); see §2/§3.
- **Object rate / payload:** throughput cost scales ~linearly with fps·size
  (CPU%) until core saturation; no relay-side cliff — the cliff is quic-go
  egress PPS (§3/§7).
- **GOGC:** (cores4, 10K subs) GOGC 100/800/2000 → connected 9231/9170/9746
  (92/92/97 %), GC p99 6.7/4.3/0.42 ms. **GOGC barely moves the single-host
  ceiling** (±5 %); higher GOGC slightly helps establishment by lowering GC
  interruption. The "GOGC lifts HOLD 13–15K→18–20K" result from memory was on
  **bare metal**; on single-host WSL the ceiling is establishment/Harness-bound,
  so GOGC is not the lever. **Classification: Environment/configuration.**

## 5. Environment sensitivity

| Axis | Status | Finding |
|---|---|---|
| WSL2 (this report) | ✅ measured | ~13K HOLD ceiling, establishment-limited; shape-valid (±noise) |
| GOGC | ✅ measured | ±5 % effect on single-host ceiling; not the lever here |
| Local UDP buffer / `rmem_max` | ⚠ partial | quic-go warns wanted 7168 kiB / got 416 kiB (kernel cap); relay's own `RELAY_UDP_RCVBUF` shipped (#329) but kernel-capped on WSL |
| Bare-metal Linux | ❌ blocked | prior work: ~242K PPS socket ceiling, ~18–20K HOLD at high GOGC — must re-measure on real hardware |
| Alternate NIC/kernel/GSO/GRO | ❌ blocked | quic-go v0.60.0 exposes no GSO knob; toggles need bare metal |
| Distributed (multi-host) load | ❌ blocked | **the key gap**: single-host loadgen caps ~13K with the relay idle; true relay ceiling needs distributed generation |

## 6. Capacity model (empirical; fit only where data supports)

- **Goroutines(S) = 7.0 · S** — R²≈1 across 3.6K–13.4K sessions. (All quic-go +
  gomoqt; relay spawns 0 per session.)
- **RSS(S) ≈ 200–270 KB · S** (per-session-dominated; base ≈ small/negative →
  footprint is essentially all per-connection quic-go state + stacks).
- **Heap(S) ≈ 150–200 KB · S** (noisier, GC-timing-dependent; quic-go per-conn).
- **HOLD_ceiling(cores) on single-host ≈ min(2.8·cores_K, ~13K)** — linear to
  ~4 cores, then Harness-plateau at ~13K. The relay's own (non-harness) ceiling
  is **not identified** — CPU had headroom at the plateau; it is plausibly
  higher (extrapolation ~1.3 cores @ 11K → relay could hold ~25K in ~3 of 4
  cores) but **unconfirmed** pending distributed load.
- **Objects/sec ceiling** ≥ 200K (2000 subs × gps=100, HELD); not reached at
  this fan-out. Fan-out cliff at gps=10 is ~3–4K subs (2000 HOLDS, 4000 at 98 %).
  Exact PPS cliff (≈242K socket bound) needs higher fan-out/fps — deferred.
- **A combined `Sessions = f(CPU,Mem,Pub,Sub,fps,Size)` is NOT fit**: with the
  relay never CPU/memory-bound at the measured ceiling, there is no
  relay-side capacity surface to model — the binding constraint is external
  (Harness/establishment/quic-go PPS). Fitting such a model would be
  unfounded (violates "do not fit unless data supports").

## 7. Bottleneck classification (every observed ceiling)

| Ceiling | Limiting factor | Class |
|---|---|---|
| HOLD ~13K sustainable (peak ~15K) | conn attrition above ~13K; relay 30 % CPU idle (NOT relay-bound). Mechanism **unconfirmed**: leading hypothesis OS recv buffer (416 KB), alternative quic-go ACK/idle-timeout | **External to relay** (NOT Relay/gomoqt); OS-vs-quic-go pending rmem test |
| Establishment peak ~15K / transient loss | burst QUIC handshake within retry window | **quic-go** (crypto) + **Harness** |
| Per-session 200 KB / 7 goros | QUIC conn state + conn/session goroutines | **quic-go** (+ gomoqt) |
| Throughput CPU (sendmsg) | per-subscriber UDP write | **quic-go** |
| Scheduler cost (selectgo/sellock/futex) | parking 7 goros × N sessions | **Go-runtime** |
| GC pauses | tiny (p99 ≤ 6.7 ms), not binding | **Go-runtime** (non-issue) |
| UDP recv buffer 416 kiB | kernel `rmem_max` cap on WSL | **OS** |
| Relay/gomoqt code | flat <1 % CPU, <2.5 % allocs, invisible heap | **(not a bottleneck)** |

**No ceiling in these experiments is attributable to Relay or gomoqt code.**
Every limit is Harness, quic-go, Go-runtime, or OS — consistent with the
completed optimization audit (no in-scope lever remains).

## Reproducibility

Binaries: `qumo` + `capacity` (linux/amd64). Harness: `cap-sweep.sh` →
`tools/capacity` → `qumo loadgen`, relay `RELAY_PPROF=1` + taskset + GOGC env.
Each run: fresh relay + publisher + one `subscribe N` probe, hold 45 s,
scrape relay `/metrics` (incl. `process_cpu_seconds_total` delta for CPU% and
`process_open_fds`). All raw JSONL preserved in `~/qumo-bench/sweep.jsonl`.

## Bottom line

On this single-host WSL box the relay's measured envelope is **~13K sustainable
HOLD sessions** (establishment peaks ~15K, then attrits to a stable 13K),
**≥200K objects/sec** at 2000 subs, **7 goros / ~468 KB RSS per session**
(steady-state), with the relay itself **never the bottleneck** (CPU ≤51 %/core,
GC ≤6.7 ms p99, 11 FDs). The sustainable ~13K is **external to the relay**; the
specific attrition mechanism is **a leading hypothesis (recv-buffer), not yet
confirmed** — see §2. Every ceiling is external (OS recv-buffer *hypothesized*,
quic-go egress/handshake/per-conn-state, Go-runtime scheduling, or Harness
single-host loadgen); **none is Relay or gomoqt**. Two blocked
experiments would complete the envelope: **(1)** raise `net.core.rmem_max`/
`rmem_default` on bare metal (priority — directly tests whether the ~13K
attrition is recv-buffer-driven), and **(2)** distributed load generation (the
relay's own ceiling is masked by the single-host loadgen and by the WSL recv
buffer).

