# Bottleneck attribution

Where CPU time, latency, and waiting actually accumulate in the relay and in
gomoqt — and the optimization candidates that were tried and refuted. This is
the core technical finding of the cycle.

The short version: the active-fan-out knee is a **per-subscriber fan-out drain**
— a service-rate limit, not lock contention or GC. The broadcast primitive that
wakes subscribers is essentially free (0.05 % of CPU). gomoqt is a thin,
0-allocation layer whose internal blocking is zero. The cost lives in quic-go's
per-connection work and the UDP send syscall, which neither qumo nor gomoqt can
reduce without GSO (environment) or fewer connections (topology).

## The fan-out path

One frame's lifetime through the relay, stamped with the `instrument` build tag:

```
publisher → ingest (A) → ring residence (R = R.fill + R.wake) → open (O) → write (C) → QUIC → subscriber
```

| Stage | Measures | Behaviour vs. subscribers | Owns latency? |
|---|---|---|---|
| A ingress | clone + publish | flat, µs | no |
| R.fill | reserve → broadcast | flat ~6–8 µs | no |
| **R.wake** | broadcast → egress pickup | **scales 0.44 → 44.6 ms** | **yes** |
| O open | `OpenGroupAt` (uni-stream) | µs service, off critical path | no |
| C write | `WriteFrame` | flat µs | no |
| residual | quic-go send + wire | ~69 % of p99 | partly |

At 1000 subscribers the 227 ms p99 decomposes into roughly **31 % thundering-herd
egress drain** (R.wake ≈ subscribers × ~30 µs ÷ cores) and **69 % quic-go
send/recv plus scheduler residual**. The latency is time spent *waiting to be
scheduled after the broadcast wakes every egress goroutine* — not stream-open,
write, GC, or locks.

## CPU ranking

Relay knee profile (N = 1500, real quic-go, Linux):

| Region | Share | Class |
|---|---|---|
| Go runtime scheduler/sync (`futex`, `selectgo`, `lock2`, `schedule`) | ~40 % | runtime |
| quic-go `Conn.run` (per-connection event loops) | ~30 % | quic-go |
| `sendmsg` / UDP send syscall | ~16 % | quic-go + kernel |
| quic-go packet packing + AES | ~10 % | quic-go/crypto |
| relay `deliverGroup` | ~11 % | relay (of which `OpenGroupAt` only ~3 %) |
| GC | ~2 % | runtime (non-factor) |
| relay locks | ~0 | (non-factor) |

The same shape appears in a **pure-gomoqt** real-QUIC profile, where gomoqt's own
code is only ~3–4 % of CPU (see the gomoqt section below).

## Where execution waits (not just where CPU goes)

CPU profiles cannot show waiting, so the cycle also captured a `runtime/trace`
and block/mutex profiles. The attribution is unambiguous.

Block profile at the relay knee — total 132.8 h of sampled blocked time:

| Blocking primitive | Share | Flows through |
|---|---|---|
| `runtime.selectgo` | 83 % | quic-go `AcceptStream` / `ReceiveStream.Read` |
| `runtime.chanrecv1` | 16 % | quic-go `ReceiveStream.Read` |

Every gomoqt goroutine that blocks is parked **inside** a quic-go Accept or Read
call. The only gomoqt select with measurable blocking is `AnnouncementReader` —
the control plane, not the media path. The media data path (`encode`,
`WriteFrame`, `ReadFrame`) has **zero** internal blocking.

Mutex contention remains irrelevant: the mutex profile is ~99 % test harness,
and the production paths that appear are *parking* on quic-go, not contended
critical sections.

## The refuted premise: "broadcast() wakes all subscribers"

A natural reading of the sched-latency profile is that the per-frame
`broadcast()` — which closes a notification channel and wakes every egress
goroutine — is an expensive O(N) operation. The cycle tested this directly and
**refuted** it.

- The entire `broadcastNotify` path (notify + close + lazy-init) is **0.05 %**
  of knee CPU.
- A standalone model: `close(ch)` with 1000–2000 parked waiters costs ~0
  (nanoseconds per waiter); waking and running all of them to completion is
  ~13 µs at 1000, ~143 µs at 2000.
- The real R.wake is 3–44 ms — a 1000× gap the wakeup cannot explain.

The wakeup and the goroutine count are microsecond-scale. The milliseconds are
the **per-connection quic-go work** each woken goroutine then competes for cores
to do. The earlier "51 % = broadcastNotify.notify" reading was a sched-latency
misattribution: that profile charges downstream blocking to the wake point.

### Optimization candidates tried

| Candidate | Hypothesis | Measured result | Decision |
|---|---|---|---|
| Sharded broadcast channels | split the wake herd | `close()` is ~0; sharding a zero cost; notify 0.05 % of CPU | **rejected** |
| Worker-pool egress | fewer runnable goroutines | drain is µs-scale; the ~1000 connection goroutines are untouched; reintroduces head-of-line blocking | **rejected** |
| Wake only caught-up subscribers | skip redundant wakes | in steady audio every subscriber needs every frame | **rejected** |
| GOMAXPROCS / pinning | relieve scheduler pressure | capacity plateaus at ~4–8 cores (see [scaling](scaling.md)) | **rejected** |
| Per-frame fan-out work (copies/atomics/locks) | trim the hot path | locks ~0, GC 2 %, ring lock-free, already lean | **rejected** |

None survived measurement. The discipline is to discard non-improvements rather
than ship them as wins.

## The gomoqt layer

gomoqt was audited three ways — focused hot path, comprehensive (CPU/alloc/
escape/syscall), and waiting/latency. All converge: **gomoqt is performance-
complete for its current architecture.**

**Per-frame encode/write is already optimal.** `Frame.encode` reserves an 8-byte
length prefix in-place and issues a single zero-copy `Write`; `WriteFrame` is a
thin wrapper. Both are 0-allocation, 0-copy, with no escapes. There is nothing
to optimize on the fan-out-scaling path.

```text
BenchmarkGroupWriter_WriteFrame/size-1024    11.4 ns/op    0 B/op    0 allocs/op
BenchmarkFrame_Encode/1KB                    18.5 ns/op    0 B/op    0 allocs/op
```

**`OpenGroup` is the only allocating hot path** (7 allocs, ~365 ns), but ~85 %
of those allocations are semantic — the GroupWriter's cancellation context,
which is correct and out of scope. The two avoidable throwaway encode buffers
were addressed in a candidate that coalesced the group-open into one `Write`
(PR attempt), but it measured **neutral-to-negative** (allocs 7 → 6, yet
B/op 328 → 352 because the stack buffer escapes through the `io.Writer`
interface) and was discarded.

**gomoqt's own CPU share is ~3–4 %** in a pure-gomoqt real-QUIC profile:

| Region | Share | Owner |
|---|---|---|
| UDP send syscall (`sendQueue` → `sconn.Write` → `sendmsg`) | ~44 % | quic-go + kernel |
| quic-go `Conn.run` | ~19 % | quic-go |
| Go runtime scheduler | ~15 % | runtime |
| `sendPacketsWithoutGSO` (GSO is off) | ~9 % | quic-go |
| packet packing + AES | ~6 % | quic-go/crypto |
| **gomoqt's own code (total)** | **~3–4 %** | gomoqt |

**gomoqt's internal blocking is zero.** The `runtime/trace` attribution:

| Category | Where goroutines park | gomoqt flat share |
|---|---|---|
| network | `UDPConn.ReadFrom` (quic-go recv) | 0 |
| sync (chan/select) | `selectgo`/`chanrecv` via quic-go Read/Accept | 0 |
| syscall | `sendmsg` via quic-go `sendQueue` | 0 |
| scheduler | quic-go send-path handoff | 16 µs (noise) |

gomoqt functions appear only in the *cumulative* stacks — they sit on the call
stack above the quic-go park site. gomoqt is a pass-through on the wait path,
never the parking site.

## Conclusion

The relay's active-fan-out knee is a fan-out drain, owned jointly by quic-go's
per-connection work and the Go scheduler. gomoqt is a thin, optimal layer above
it. The levers that survive are all **below or beside** the application code:

- **GSO** (bare-metal Linux) — attacks the 44 % send syscall directly. quic-go
  v0.60 exposes no public knob; it is an environment capability.
- **Hierarchy / topology** — fewer connections per node, proportionally less
  per-connection work. See [scaling](scaling.md).
- **quic-go internals** — explicitly out of scope.

## See also

- [Baseline](baseline.md) — the numbers this attributes.
- [Scaling](scaling.md) — why cores plateau and what hierarchy buys.
- [Optimization ledger](optimization-ledger.md) — the full attempt log.
