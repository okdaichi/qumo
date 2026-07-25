# gomoqt Hot-Path Audit

**Scope:** focused perf audit of `github.com/qumo-dev/gomoqt` (cloned at `d:/gomoqt`,
HEAD `22fa559`) to verify whether any **low-risk hot-path improvement** exists before
deeper quic-go / topology work. **Method:** perf-engineer discipline — measure first,
hypothesize, adjudicate from evidence, ship no neutral change as a win.
**Date:** 2026-07-25.

**Headline:** gomoqt's per-frame hot path is **already allocation-free and optimal**.
The only allocating hot path is `OpenGroup`, and ~85 % of its allocations are
**semantic** (the GroupWriter's cancellation context) that the brief forbids touching.
The one avoidable candidate (coalescing the group-open writes) is **measured-neutral**
and **not adoptable on available evidence**. **Conclusion: gomoqt is not the
bottleneck — further work belongs in quic-go / topology.** This confirms the qumo
profiling finding ([`FANOUT_OPTIMIZATION_RESULTS.md`], [`LATENCY-ATTRIBUTION.md`]).

---

## 1. Current baseline

### Benchmark inventory (reused — none created)
gomoqt ships a full bench suite in `moqt/` (no new benchmarks added this audit):

| File | Hot-path benches |
|---|---|
| `frame_benchmark_test.go` | `Frame_Encode`, `Frame_Decode`, `Frame_Reuse`, `Frame_Clone`, `Frame_WriteTo` |
| `group_benchmark_test.go` | `GroupWriter_WriteFrame`, `GroupReader_ReadFrame`, `*_MemoryAllocation` |
| `track_benchmark_test.go` | `TrackWriter_OpenGroup`, `*_ConcurrentOpenGroup`, `*_MemoryAllocation` |
| `egress_benchmark_test.go` | `Egress_Saturating` (real-QUIC, `-short` excluded) |
| `fanout_benchmark_test.go` | `FanOut_ViewerConnections{,Latency}`, `FanOut_TracksPerConnection` (real-QUIC, `-short` excluded) |

### Command
```bash
cd d:/gomoqt
go test ./moqt/ -run='^$' -benchmem -count=5 -benchtime=200ms \
  -bench='BenchmarkFrame_Encode$|BenchmarkGroupWriter_WriteFrame$|BenchmarkTrackWriter_OpenGroup$'
```

### Baseline numbers (Windows, GOMAXPROCS=16, current code)  [FACT]

**Frame encode / write path — already optimal (0 allocs):**

| Bench | size | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| `Frame_Encode` | 64 B | 11.1 | 0 | **0** |
| `Frame_Encode` | 1 KB | 18.5 | 0 | **0** |
| `Frame_Encode` | 16 KB | 209 | 0 | **0** |
| `Frame_Reuse` | 1 KB | 12.9 | 0 | **0** |
| `GroupWriter_WriteFrame` | 1 KB | 11.4 | 0 | **0** |
| `GroupWriter_WriteFrame` | 64 KB | 12.9 | 0 | **0** |

`Frame.encode` reserves an 8-byte prefix in `buf` so the varint length is written
in-place, then issues a **single `w.Write(buf[start:end])` with zero payload copy**.
`WriteFrame` is a thin wrapper over it. **Nothing to optimize here.**

**`Frame_Clone`** = 2 allocs / op (`NewFrame` + `init`'s `make`) — but this is qumo's
per-subscriber deep copy, already analyzed there: it is 1 of (N+2) copies,
egress-dominated, and copy-elimination was **falsified** (qumo
[`F2_copy_elimination`]). Out of scope.

**`TrackWriter_OpenGroup` — the only allocating hot path:**

| groups | ns/op | B/op | allocs/op |
|---|---|---|---|
| 10 | 369 | 328 | **7** |
| 100 | 365 | 328 | **7** |
| 1000 | 361 | 328 | **7** |

Flat across group counts (no per-group state leak). 7 allocs / 328 B per open.

---

## 2. Hot-path analysis

### Allocation breakdown of `OpenGroup` (mem profile, `groups-10`)  [FACT]

| Site | share | class |
|---|---|---|
| `context.WithCancelCause` + `withCancel` | ~39 % | **semantic** — GroupWriter cancellation ctx |
| `context.WithValue` | ~14 % | **semantic** — stream-type ctx key |
| `newGroupWriter` (struct) | ~16 % | **semantic** — the GroupWriter itself |
| `StreamType.Encode` (`[]byte{byte(stm)}`) | ~7.5 % | avoidable — 1-byte throwaway slice |
| `GroupMessage.Encode` (`make([]byte,…)`) | ~1.9 % | avoidable — throwaway buffer |
| bench harness | ~18.5 % | artifact (FakeQUICSendStream ctx) |

**~85 % of OpenGroup allocations are semantic** (GroupWriter lifecycle / cancellation
context). The brief explicitly forbids changing Group behavior, so these are
out of scope. Only **~2 of 7 allocs are avoidable** throwaway encode buffers.

### Where the OpenGroup cost really is  [FACT]
The two `.Encode(stream)` calls in `openGroupWithSequence` are **two separate
`stream.Write` calls** to the QUIC SendStream: `StreamTypeGroup.Encode` then
`GroupMessage.Encode`. On a *real* quic-go stream each Write acquires the stream
mutex, checks flow control, and copies into the sendQueue. On the fake
in-memory bench stream these are trivial (hence ~365 ns total), so the microbench
**cannot** see the real-quic-go cost — it is dominated by the (unmeasured-here)
`OpenUniStreamSync` + sendQueue interaction.

---

## 3. Experiments

### Candidate A — Coalesce group-open writes (worktree `perf/coalesce-group-open`)  [REVISIT]

**Hypothesis (pre-registered):** combining the stream-type byte and the GROUP message
into one stack-buffered `stream.Write` removes one throwaway encode allocation *and*
halves the per-open Write calls into quic-go (2 → 1), reducing sendQueue / stream-lock
interaction. Wire output byte-identical. CONFIRMED only if allocs/op drops with no
bytes-op regression on the microbench **and** a real-quic integration A/B shows a
latency/throughput gain; else REFUTED.

**Implementation** (worktree `d:/gomoqt-coalesce`, branch `perf/coalesce-group-open`,
isolated, not merged):
- `message.GroupMessage.AppendEncode(b []byte) []byte` — append-style encoder sharing
  the exact wire bytes of `Encode`. `Encode` now delegates to it (single source of
  truth).
- `message.MaxVarintLen = 8` constant for sizing the stack buffer.
- `track_writer.openGroupWithSequence`: one `var hdr [1+3*MaxVarintLen]byte`, append the
  type byte + `AppendEncode`, single `stream.Write(buf)`.
- New test `TestGroupMessage_AppendEncode_WireIdentical` spans all varint length
  boundaries (1/2/4/8-byte) for both fields and asserts byte-equality with `Encode`.
  **Passes.** `go vet` + `go build` clean; `moqt/internal/message` tests pass.

**Microbench result (fake in-memory stream, before → after):**  [FACT]

| metric | before | after | delta |
|---|---|---|---|
| allocs/op | 7 | **6** | −1 |
| B/op | 328 | **352** | **+24 (regression)** |
| ns/op | ~365 | ~358 | flat |

**Reading:** removed one throwaway buffer (`GroupMessage.Encode`), but the 25-byte stack
array escapes through the interface `stream.Write` (unavoidable — any buffer passed to
`io.Writer.Write` escapes), so **bytes/op regressed +24**. ns/op flat. On the microbench
this is **neutral-to-slightly-negative** — not a win.

**Why it can't be adjudicated in gomoqt:** every gomoqt real-QUIC fanout bench uses
`fpg = 256` ("amortizes OpenGroup") *by design* (`fanout_benchmark_test.go:76,114,152`).
So none can isolate an OpenGroup-per-frame change. The only workload where OpenGroup
matters per-frame is **audio (1 frame/group)**, measured in **qumo's** `FanoutSingleRelay`
— where OpenGroupAt is already established as **~3 % of knee CPU and off the critical
latency path** (the `openTimeout` work was latency-neutral; [`LATENCY-ATTRIBUTION.md`]).

**Decision: REVISIT — do not adopt on current evidence.**
- Wire-identical (proven), low-risk, and *plausibly* a small production win (one fewer
  quic-go sendQueue/stream-lock interaction per group-open).
- But **not justified by measurement**: microbench neutral-to-negative on bytes; no
  gomoqt bench can confirm it; qumo shows OpenGroupAt is off the critical path.
- To adopt would require a **real-quic-go `fpg=1` A/B** that does not exist. Per the
  discipline, a neutral microbench + unmeasured hypothesized benefit = do not ship.
- Worktree/branch retained (per "keep every experiment isolated"); not merged.

### Candidates B–D — not pursued (measured-negative upstream)
- **Buffer reuse / copy elimination (B):** WriteFrame and Frame.Encode are already
  0-alloc, 0-copy. Nothing to reuse.
- **OpenGroupAt (C):** the qumo `openTimeout` cycle already cut its allocations
  (272 B/4 allocs → 0/0 on the per-delivery timer) and was **latency-neutral**;
  qumo profiles put OpenGroupAt at ~3 % of knee CPU. Spending more here is not
  justified.
- **API/internal overhead (D):** no interface/wrapper overhead visible in the hot
  path (encode is direct, no conversions). Not in any profile.

---

## 4. Final conclusion

1. **Does gomoqt have a meaningful hot-path bottleneck?** **No.** The per-frame path
   (`WriteFrame`, `Frame.Encode`) is allocation-free and copy-free. The only allocating
   hot path (`OpenGroup`) is ~85 % semantic context allocations that are correct and
   out of scope; the avoidable remainder is ~2 small throwaway buffers (~9 % of OpenGroup
   cost, which is itself ~3 % of qumo's knee CPU).

2. **Can low-risk optimizations improve qumo fanout?** **Not measurably.** The single
   candidate (Candidate A) is microbench-neutral with a bytes regression, and no
   gomoqt or qumo measurement shows OpenGroup on the critical latency path. The
   available evidence says further gomoqt hot-path work will not move qumo's fanout
   knee or p99.

3. **Is further work better spent in gomoqt or quic-go / topology?** **quic-go /
   topology.** This audit independently re-confirms the qumo finding: the dominant
   cost at high fanout is **per-connection QUIC transport** (`Conn.run` 30 %,
   `sendmsg` 16 %, packet-packing + crypto ~10 %) — below gomoqt, which is a thin
   layer over it. The levers that survive are **hierarchy/topology** (fewer
   connections per relay — already validated, 227 → 34 ms p99) and **GSO on bare-metal
   Linux** (attacks the 16 % syscall; no quic-go v0.60 knob). gomoqt is not the place
   to spend effort for fanout scalability.

---

## Process notes
- **No gomoqt benchmarks were duplicated.** The existing suite answered every question;
  the one measurement gap (a real-quic `fpg=1` OpenGroup A/B) is structural to gomoqt's
  bench design and would be a new bench — not warranted given OpenGroupAt is off the
  critical path.
- **One worktree opened** (`d:/gomoqt-coalesce`, branch `perf/coalesce-group-open`),
  isolated, **not merged**. Main `d:/gomoqt` tree untouched.
- **`performance-check` CI not triggered:** the candidate was adjudicated
  neutral-to-negative on the local microbench, so opening a PR to run the
  (fpg=256-amortized) integration benches would not change the decision. If a real-quic
  `fpg=1` bench is added later, Candidate A should be re-run there via the label.
- **Lab ≠ production:** microbench numbers are Windows/local; the *bottleneck-class*
  conclusion (gomoqt thin over quic-go transport) is portable.

_Related (qumo): [`FANOUT_OPTIMIZATION_RESULTS.md`], [`LATENCY-ATTRIBUTION.md`],
[`CURRENT_BASELINE.md`], [`OPTIMIZATION-LEDGER.md`]._
