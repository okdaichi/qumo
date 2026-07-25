# gomoqt Comprehensive Performance Audit

**Scope:** comprehensive audit of `github.com/qumo-dev/gomoqt` (HEAD `22fa559`) for
**non-architectural** optimization opportunities across the gomoqt↔quic-go boundary,
allocations, escape analysis, syscalls, the I/O pipeline, and cache locality.
**Method:** perf-engineer discipline — every conclusion backed by a profile or the
compiler; ship no neutral change. **Date:** 2026-07-25.
**Predecessor:** [`GOMOQT_HOTPATH_AUDIT.md`] (focused hot-path audit; this supersedes
and broadens it).

**Headline:** gomoqt's own code is **~3–4 % of CPU** in a pure-gomoqt real-QUIC
workload; its egress (write) hot path — the fan-out-scaling path — is **0-allocation
and 0-copy**. ~95 % of CPU and allocations live in **quic-go + UDP syscalls + the Go
runtime**, which gomoqt cannot reduce without GSO (environment) or fewer connections
(topology). The **one** actionable non-architectural candidate is already in PR #364.
**Conclusion: gomoqt is close to optimal for its current architecture** — supported by
measurement, not intuition.

Confidence tags: **[FACT]** directly measured · **[INFERENCE]** measurement-supported ·
**[UNKNOWN]** needs further experiment.

---

## 1. Current hot-path breakdown  [FACT]

Pure-gomoqt real-QUIC profile: `BenchmarkFanOut_ViewerConnectionsLatency/subs-16`
(loopback, self-signed TLS, fpg=256, 1 KB frames, GOMAXPROCS=8, real quic-go).

```
Encode → WriteFrame → stream.Write → [quic-go sendQueue → packetPacker → sconn.Write → UDP sendmsg]
```

Result: **14965 ns/op, 276 B/op, 10 allocs/op, p99 13.9 ms, 0 starved readers.**

The pipeline has **no hidden overhead**: `Frame.encode` writes the varint length into a
pre-reserved 8-byte prefix and issues a **single `Write`** of `buf[start:end]` — no
`bytes.Buffer`, no append chain, no copy, no temp slice. `WriteFrame` is a thin wrapper.
**This path is minimal.**

---

## 2. CPU usage ranking  [FACT]

From the CPU profile (cumulative), pure-gomoqt real-QUIC:

| Rank | Region | cum % | Owner |
|---|---|---|---|
| 1 | UDP send syscall (`sendQueue.Run`→`sconn.Write`→`writePacket`→`WriteTo`→`wsaSendto`/`sendmsg`) | **~44 %** | quic-go + kernel |
| 2 | quic-go `(*Conn).run` (per-connection event loop) | ~19 % | quic-go |
| 3 | Go runtime scheduler (`schedule`/`findRunnable`/`mcall`/`park_m`) | ~15 % | runtime |
| 4 | quic-go `sendPacketsWithoutGSO` (**GSO is OFF**) | ~9 % | quic-go |
| 5 | quic-go packet packing / crypto / framing | ~6 % | quic-go |
| — | **gomoqt's own code (all functions)** | **~3–4 %** | gomoqt |

gomoqt's largest individual functions: `Frame.decode` 2.94 % (reader side),
`handleSubscribeStream` 2.60 %, `WriteFrame` 2.27 %, `Frame.encode` 2.18 %,
`sendStreamWrapper.Write` 2.02 % (a one-line pass-through).

**[INFERENCE]** gomoqt is not the CPU bottleneck. Even perfect optimization of all
gomoqt code moves ≤ ~4 % — at or below the noise floor of a transport-dominated profile.

---

## 3. Allocation ranking  [FACT]

From the allocation profile (`alloc_objects`), pure-gomoqt real-QUIC:

| Rank | Site | flat % | Owner | scales with fanout? |
|---|---|---|---|---|
| 1 | webtransport-go `ReceiveStream.Read` | 31.8 % | transport | reader-side (no) |
| 2 | `UDPConn.readFrom` / `ReadFrom` | 26.8 % (cum) | transport | no |
| 3 | quic-go `handleStreamFrameImpl` | 11.3 % | transport | no |
| 4 | webtransport-go `SendStream.Write` | 9.1 % | transport | yes (egress) |
| 5 | quic-go `framer.Append` | 6.1 % | transport | yes |
| 6 | gomoqt `ReadMessageLength` | 3.4 % | gomoqt | **reader-side (no)** |
| — | gomoqt egress (`WriteFrame`, `Frame.encode`) | **0** | gomoqt | — |

**Classification:**
- **Per-frame, egress (the fanout path):** **0 allocs.** Already optimal.
- **Per-frame, ingress/reader:** `ReadMessageLength` — but its source is already a
  0-alloc `io.ByteReader` fast path (`ReadByte` per byte) with a stack `[8]byte`
  fallback; the 3.4 % is transport allocs flowing *through* it, not gomoqt allocs. A
  relay reads once and writes N times, so reader-side allocs are O(1)/frame and do not
  scale with fanout. **Not a fanout lever.**
- **Per-group, egress:** the two throwaway encode buffers — **PR #364**.
- **Per-stream / per-connection:** wrapper structs (`rawQuicSendStream`, etc.) — semantic.
- **Per-track:** `newTrackWriter` / `groupWriterManager` — semantic (construction).

**[INFERENCE]** gomoqt's egress allocation profile is already zero. There is no
allocation-reduction lever on the fanout-scaling path beyond #364.

---

## 4. Syscall ranking  [FACT]

| Syscall (Linux name / Windows equivalent) | share | reducible by gomoqt? |
|---|---|---|
| `sendmsg` / `wsaSendto` (UDP send) | **~44 %** | **No** — frequency set by quic-go's sendQueue batching; gomoqt only issues `stream.Write` |
| `recvmsg` / `wsaRecvFrom` (UDP recv) | (in read path) | No |
| `writev`/`readv` | not used (QUIC is UDP) | — |

**[FACT]** The syscall share is owned by quic-go's packetization. gomoqt's only lever to
reduce *its own* contribution to syscall frequency is to issue **fewer `stream.Write`
calls** — which is exactly what #364 does for group-open (2 Writes → 1). At fpg=256 this
is 1/256th of writes (negligible); at the audio baseline (fpg=1) it is 1/1 (real but
small, and OpenGroupAt is ~3 % of knee CPU per the qumo profile). The dominant syscall
reduction is **GSO** (`sendPackets` vs the measured `sendPacketsWithoutGSO`) — an
**environment/kernel** capability, not a gomoqt code change. quic-go v0.60 exposes no
public GSO knob.

---

## 5. Cache-locality findings  [UNKNOWN]

Not measured: hardware perf counters (`perf stat`: cache-misses, LLC-misses,
branch-misses) require bare-metal Linux; Windows/WSL2 `perf` is unreliable for this.
**Expected value is low:** gomoqt is ~3–4 % of CPU, so even eliminating every cache miss
in gomoqt code moves ≤ ~4 %. The cache-hot structures (`Frame.buf` is a contiguous
slice; `GroupWriter`/`TrackWriter` are pointer-sized handles to stream state) show no
obvious false-sharing or pointer-chasing on inspection. **Flagged [UNKNOWN], low
priority — do not pursue without a Linux `perf` run that first shows gomoqt code owning
a meaningful miss share.**

---

## 6. Escape-analysis findings  [FACT]

`go build -gcflags="-m=2" ./moqt/...` — hot-path escapes:

| Escape | Site | class | verdict |
|---|---|---|---|
| `make([]byte,…)` | `GroupMessage.Encode` (group.go:24) | per-group, avoidable | **#364** (AppendEncode) |
| `[]byte{byte(stm)}` | `StreamType.Encode` (stream_type.go:23) | per-group, avoidable | **#364** (coalesced) |
| `&GroupWriter{}`, ctx key | `newGroupWriter` | per-group, **semantic** | out of scope |
| `&TrackWriter{}`, `groupWriterManager` | `newTrackWriter` | per-track, semantic | out of scope |
| `&rawQuicStream{}`/`&rawQuicSendStream{}` | connection wrappers | per-stream, semantic | out of scope |
| `Frame.encode`/`append`/`WriteFrame` | — | **none escape** | already optimal |

**[FACT]** The only avoidable hot-path escapes are the two per-group encode buffers, both
addressed by #364. Escape analysis independently confirms the focused audit's conclusion.
The `Frame` encode path has **zero escapes** — stack-reserved prefix, single Write.

---

## 7. Adopted optimizations

**PR #364 — Coalesce group-open writes** (`perf/coalesce-group-open`, open, `performance-check` label set, CI green for build/race/changelog; benchstat run in progress).

- **Change:** `TrackWriter.openGroupWithSequence` issues one stack-buffered `Write`
  (`[type byte ++ GROUP message]`) instead of two `Encode`→`Write` calls. Adds
  `GroupMessage.AppendEncode` (wire-identical sibling of `Encode`) + `MaxVarintLen`.
- **Evidence:** escape analysis (§6) + alloc profile localize the only avoidable per-group
  escapes here; wire-identical test spans all varint boundaries.
- **Microbench:** allocs/op 7 → 6; B/op 328 → 352 (**+24, regression** — stack buffer
  escapes through the `io.Writer` interface); ns/op flat. **Neutral-to-negative on the
  fake-stream microbench.**
- **Status:** **provisional — awaiting the Linux `performance-check` benchstat.** Adopt
  only if it shows a real gain; the hypothesized benefit (one fewer quic-go
  sendQueue/stream-lock interaction per group-open) is not exercised by the `-short` CI
  suite nor by the fpg-256 benches. Honest expectation: **neutral → likely close without
  merge** unless CI surprises.

---

## 8. Rejected optimizations (measured-negative or out-of-scope)

| Candidate | Evidence | Decision |
|---|---|---|
| Remove `sendStreamWrapper.Write` interface dispatch (2 %) | one-line pass-through; removing = architectural (abstracts webtransport-go vs raw-quic) | **REJECT** (architectural, ~2 %) |
| Optimize `WriteFrame`/`Frame.encode` | 0 allocs, 0 copy, single Write, no escapes | **DONE — already optimal** |
| Optimize `ReadMessageLength` (3.4 %) | already 0-alloc `io.ByteReader` fast path; reader-side (no fanout scaling) | **REJECT** |
| `sync.Pool` for encode buffers | egress is 0-alloc; nothing to pool | **REJECT** |
| Reduce syscall frequency in gomoqt | frequency owned by quic-go sendQueue; only lever is GSO (env) or fewer Writes (#364) | **REJECT** (transport-owned) |
| Cache-locality tuning | needs Linux perf; gomoqt ~3-4 % CPU so ≤4 % ceiling | **DEFER** ([UNKNOWN]) |

---

## 9. Measured improvements

- **Adopted:** none yet confirmed (PR #364 provisional, microbench-neutral).
- **Confirmed optimal (no change needed, measured):** egress encode/write path — 0
  allocs/op, 0 copy, ~2.2 % CPU, no escapes. This is the fanout-scaling path and it is
  already at the floor.

---

## 10. Areas still worth investigating

1. **Real-quic `fpg=1` OpenGroup A/B** (the only place #364's benefit could show):
   requires a new bench (gomoqt's fanout benches all use fpg=256) — not warranted given
   OpenGroupAt is ~3 % of qumo knee CPU and off the critical latency path. **Low priority.**
2. **Linux `perf` cache-counters on gomoqt** (§5): only if a future run shows gomoqt
   owning a meaningful miss share. **Low priority.**
3. **GSO on bare-metal Linux**: the single largest addressable cost (~44 % syscall +
   `sendPacketsWithoutGSO` 9 %), but it is environment/quic-go, not gomoqt. Track via
   qumo #342 (distributed host).

---

## 11. Areas effectively "done"

- **Egress encode/write pipeline** (`Frame.encode` → `WriteFrame` → `stream.Write`):
  0-alloc, 0-copy, single Write, no escapes. **Optimal.**
- **Per-group-open allocation reduction**: addressed by #364 (the only avoidable escapes).
- **Reader-side varint decode** (`ReadMessageLength`): already 0-alloc fast path.
- **Boundary wrapper layer**: thin pass-throughs; removing is architectural, ~2 %.

---

## Final conclusion

**Does gomoqt have meaningful non-architectural optimization headroom?** **No — not
beyond PR #364, which is itself microbench-neutral and provisional.** The measurements
(CPU profile, alloc profile, escape analysis) converge: gomoqt is ~3–4 % of CPU in a
real-QUIC workload, its fanout-scaling egress path is already 0-allocation/0-copy, and
~95 % of cost is quic-go + UDP syscalls + runtime — below gomoqt's layer.

**Where further work belongs:** **quic-go / topology / environment**, not gomoqt:
- **GSO** (bare-metal Linux) attacks the 44 % syscall directly — environment.
- **Hierarchy / topology** reduces per-connection transport cost — deployment.
- These are the qumo-side levers ([`FANOUT_OPTIMIZATION_RESULTS.md`],
  [`MULTI_NODE_SCALING.md`]).

A "gomoqt is already close to optimal for its current architecture" conclusion is the
**measurement-supported** outcome of this audit.

---

## Process notes
- **No new benchmarks created.** The existing suite (`FanOut_ViewerConnectionsLatency`,
  `TrackWriter_OpenGroup`, `Frame_*`, escape analysis) answered every question.
- **One worktree / PR** (#364, from the prior focused audit) — isolated, not merged.
  gomoqt main tree untouched. **No new worktrees were warranted:** every remaining
  candidate is measured ≤ ~2 % or transport-owned, below the optimization noise floor.
- **`performance-check` CI** triggered on #364; result pending (will be folded in as a
  final confirmation; expected neutral).
- **Lab ≠ production:** profiles are Windows loopback; the *bottleneck-class* attribution
  (transport-dominated, gomoqt thin) is portable and matches the Linux qumo knee profile.

_Related: [`GOMOQT_HOTPATH_AUDIT.md`], [`FANOUT_OPTIMIZATION_RESULTS.md`],
[`LATENCY-ATTRIBUTION.md`], [`MULTI_NODE_SCALING.md`]._
