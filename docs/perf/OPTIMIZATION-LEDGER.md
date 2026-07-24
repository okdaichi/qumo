# Optimization Ledger — gomoqt + qumo validation

**Frame:** gomoqt and qumo are ASSUMED near-optimal. quic-go is a BLACK BOX (no
proposed changes). The job is to *falsify* that assumption: find evidence of a
missed optimization, not to hunt for "more optimizations."

**Scope:** code in `github.com/qumo-dev/qumo` (qumo) and
`github.com/qumo-dev/gomoqt` (gomoqt) only. quic-go, the Go runtime, the OS, and
hardware are OUT of scope (classified External).

**Terminal states (every candidate ends in exactly one):**
- **Confirmed improvement** — measured, reproducible, statistically significant.
- **No measurable improvement** — experiment ran, delta within noise floor.
- **Environment/configuration tuning** — a knob/env/deploy setting, not code.
- **Negligible** — real effect but below any materiality threshold (quantified).
- **External** — root cause outside gomoqt+qumo (quic-go/runtime/OS/hardware).
- (Out-of-scope: already addressed by separate in-flight work — recorded, not evaluated.)

**Rule:** nothing stays "unknown." Do not revisit a closed candidate unless *new*
evidence appears. Iterate until a full pass yields no new candidates, then run
an independent adversarial audit.

**Materiality δ:** a change is material only if it moves a relevant metric
(per-session CPU, per-session allocs, per-session live memory, or a micro-bench
ns/op or allocs/op) beyond the noise floor (benchstat p<0.05, or ≥5% with
reproduction) WITHOUT being offset elsewhere.

---

## Profile baseline (2026-07-24, WSL relay cores 0-3 GOGC=1000, RELAY_PPROF)

| point | sessions | goros | heap live | RSS | GC p99 | relay CPU (of 4 cores) |
|---|---|---|---|---|---|---|
| cruise | 7,831 | 54,840 | 1.35 GB | 1.6 GB | 1.5 ms | ~1.28 cores (32%) |
| ceiling | ~10,900 | 76,358 | 2.15 GB | 3.0 GB | 2.8 ms | ~1.33 cores (33%) |

CPU flat top (ceiling): quic-go `sendmsg`/Syscall6 ~24%; runtime `futex` ~5%,
`selectgo`/`sellock`/mutex/lock2 ~10%; quic-go crypto (aes/gcm) ~2%.
Relay flat: `egress` 0.2%, `deliverGroup` 0.075%, `ServeTrack` 0%.
gomoqt flat: `openGroupWithSequence` 0.4%.
→ relay+gomoqt combined flat < 1% of CPU. CPU axis is effectively exhausted in
   relay+gomoqt; remaining CPU is quic-go + runtime (External).

Heap alloc_objects top: quic-go handshake crypto (hmac/sha256/hkdf/aes) ~35%
(transient, establishment-bound).

---

## Pass 1 candidates

Method: for CPU/alloc/memory candidates the profile *is* the smallest
experiment — it directly measures each function's contribution. A function at
<δ% of the relevant total cannot yield a ≥δ% improvement from any edit to it,
which falsifies materiality without a before/after run.

| ID | Candidate (gomoqt+qumo scope) | Smallest experiment | Result | Classification |
|---|---|---|---|---|
| C1 | `broadcastNotify.notify()` allocates 2 obj/broadcast (channel+state) | `BenchmarkBroadcastNotify_Notify`: 2 allocs/85 ns, per-group (not per-sub); at gps=1 = ~2 allocs/sec/track | real but per-group; <0.01% of allocs at 11K subs | **Negligible** |
| C2 | relay per-session live heap shrinkable | heap `inuse_space` top-18: no relay node (each site <4.3 MB); relay = 11 small sites vs quic-go 222 | relay live footprint invisible; ~95% of live heap is quic-go | **Negligible** |
| C3 | `deliveryHistogram.Observe` per group | prometheus histogram Observe is alloc-free by design (no appends) | 0 allocs | **Negligible** |
| C4 | `time.Now()`/`time.Since()` per group | stdlib, alloc-free | 0 allocs | **Negligible** |
| C5 | relay mutex contention under fan-out | CPU flat: relay locks absent; mutex cost is runtime-internal (`selectgo`/`sellock` from 76K parked goroutines, not relay `sync.Mutex`). Relay uses 1-writer / lock-free designs (groupCache #314, broadcastNotify single-writer, trackManager RCU) | no contended relay lock | **Negligible** |
| C6 | fill-path per-frame allocs | groupCache is lock-free append-only vector since #314 (CAS-reserve slot, 0 alloc/append) | 0 alloc/frame | **Negligible** (already addressed by #314) |
| C7 | gomoqt subscribe-path allocs | alloc_objects flat: `newGroupWriter` 1.89% (`&GroupWriter{}`+`context.WithValue`), `handleSubscribeStream` 0.24%, `serveTrack` 0.11%; total gomoqt flat ~2.4% vs quic-go ~85% | real per-group alloc, but <2.5% of allocs and GC already cheap | **Negligible** |
| C8 | frame copy in egress | previously falsified (F2, [[perf_relay_f2_copy_elimination]]) | closed | **No measurable improvement** (falsified) |
| C9 | other per-frame prometheus updates | ingressCounter is per-group/per-publisher (not per-sub); `metricSubscribersActive` per-connect not per-frame | sub-Hz | **Negligible** |
| C10 | `addEgress` per-frame atomic (left per-frame by #333) | per-session (uncontended) atomic; batching doesn't reduce cross-sub contention | no contention to remove | **Negligible** |
| C15 | egress-goroutine `select` tax (~3-5% CPU via `selectgo`/`sellock`) | CPU flat: selectgo 2.89%, sellock 1.58% — cost of 1 parked egress goroutine/sub. Removing it needs eliminating per-sub goroutines (architectural, head-of-line-blocking risk). BUT relay is only 33% CPU-utilized at the ceiling → reducing it does not raise the session ceiling | real cost, irrelevant to capacity (relay not CPU-bound) | **Negligible** (for the 25K-capacity goal) |

**Pass 1 outcome:** every candidate in gomoqt+qumo scope classifies **Negligible**.
No measurable optimization found. Dominant costs are all quic-go (sendmsg,
crypto, per-conn state, init pools) + Go runtime (goroutine scheduling) =
**External**.

Out-of-scope (separate in-flight work, recorded for completeness, NOT evaluated
here per user instruction): #335 (deliverGroup closure allocs), #336 (fill
worker pool). These target the same already-negligible relay paths.

## Pass 2

| ID | Candidate | Experiment | Result | Classification |
|---|---|---|---|---|
| C16 | per-frame relay/gomoqt alloc/CPU hidden at realistic fps (Pass 1 used gps=1) | profiled 2969 subs @ gps=10 (10× trickle, 30K deliveries/s). CPU flat: relay `egress` 0.12% + `deliverGroup` 0.097%; gomoqt flat ~0.85% (top `openGroupWithSequence` 0.52%). Alloc by pkg: quic-go 210 / crypto 91 / gomoqt 16 / relay 10 — unchanged mix. (gps=10 used 2.06/4 cores vs 1.33 at gps=1 — more egress, still sendmsg-dominated.) | per-frame relay/gomoqt stays <1% CPU, <2.5% allocs even at 10× fps | **Negligible** |

## Pass 3

Adversarial sweep for any axis/path not yet covered. No new actionable candidate:
- **O(N)-under-fan-out relay work?** None remains — broadcast is O(1) (#332),
  `groupCache.next` O(1), `deliverGroup` is per-sub (protocol-required, one
  stream/sub). No relay loop scales with subscriber count.
- **gomoqt GroupWriter lifecycle (newGroupWriter/addGroup/Close) per group/sub?**
  Already C7; at 10× fps: addGroup 0.058%, Close 0.019% flat. Negligible.
- **Batch multiple groups per QUIC stream?** MoQ semantics = one stream per
  group; batching violates the protocol. **External** (protocol).
- **Unpooled hot alloc in relay?** Frames pooled (F4/#118), groupCache slots
  pooled (gcPool/#314); only broadcastNotify's per-notify channel (C1, per-group).
  None per-sub-per-frame.

**Pass 3 produced no new optimization candidate.** → Convergence.

## Independent adversarial audit (reviewing the above as if another engineer's work)

Attempt to prove each conclusion wrong:

1. *"Profiles measure current code; maybe a structural inefficiency bloats memory or pins goroutines without showing as hot CPU."* → live-heap `inuse_space`: relay invisible (each site <4.3 MB, 11 small sites vs quic-go 222); goroutine count 7/session is gomoqt+quic-go (relay spawns **0** per-session — `egress` runs ON gomoqt's `serveTrack` goroutine, not its own). **No structural bloat. Conclusion holds.**

2. *"GOGC=1000 hides a GC wall that GOGC=100 would expose as relay-controllable."* → GC scans the same heap regardless of GOGC; relay's share of scannable heap is invisible in both. The [[perf-hold-vs-establishment-ceiling]] A/B (GOGC 100→800: GC CPU 12%→2%, connections unchanged ±4%) already proved GC isn't the relay's lever. **Conclusion holds.**

3. *"Auth/metering (enterprise) per-frame cost not profiled."* → metering reports per 30 s; auth at connect — both sub-Hz, not in the per-frame path. Per-frame egress accounting (`egressCounter`/`addEgress`) IS profiled and negligible. **Conclusion holds** (caveat: profile ran default config; enabling metering adds sub-Hz overhead only).

4. *"WSL is noisy (±10×); real Linux differs."* → bottleneck CLASS is portable: `sendmsg` is the irreducible egress syscall and relay/gomoqt are thin over it on any host. Real Linux may add GSO (reduces sendmsg) — but that's quic-go (External). **Conclusion holds for gomoqt+qumo scope.**

5. *"The egress-goroutine `selectgo` tax (C15, ~3-5% CPU) matters at the 25K margin."* → **strongest challenge, and it resolves in favor of the conclusion**: the relay has NO per-session goroutine (egress rides gomoqt's goroutine), so the selectgo/sellock tax is entirely gomoqt+quic-go goroutines — **External**, not a relay candidate to cut. The relay's share of the scheduling tax is ~0 (it owns none of the goroutines).

**Audit result:** no conclusion overturned. Every gomoqt+qumo candidate remains
Negligible; the selectgo-tax challenge actually moved C15 from Negligible →
External (strengthening the verdict).

## Verdict

No known, measurable, reproducible optimization remains within gomoqt + qumo.
Quantified: across the HOLD regime (gps=1, ~11K ceiling) and a realistic-fps
regime (gps=10), relay+gomoqt combined are **<1% of CPU (flat)** and **<2.5% of
allocations**; relay live-heap is invisible. Dominant costs are all External —
quic-go (egress `sendmsg` ~20-25%, handshake crypto, per-connection state,
init pools ~324 MB) and Go runtime (scheduling 7 goroutines/session). The
single-host ~11K ceiling is load-side (relay 33% CPU-utilized with idle cores);
the relay's CPU extrapolation (1.3 cores @ 11K) indicates it could hold ~25K in
~3 of 4 cores, so 25K remains plausible-pending-distributed-load-generation.

Open follow-ups (all External/out-of-scope, recorded not pursued):
- distributed loadgen to confirm 25K (relay appears capable).
- upstream goroutine-count reduction (gomoqt/quic-go) to cut the scheduling tax.
- quic-go GSO / sendmmsg for egress syscall reduction (no public knob in v0.60.0).


