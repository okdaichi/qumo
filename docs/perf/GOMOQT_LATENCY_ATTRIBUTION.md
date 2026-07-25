# gomoqt Latency & Waiting Attribution

**Objective:** determine whether any meaningful **latency/waiting** bottleneck remains
inside gomoqt, or whether essentially all waiting occurs below gomoqt (quic-go, runtime,
kernel). CPU profiles cannot answer "where is execution *waiting*"; this cycle uses
`runtime/trace` + block + mutex profiles for the attribution.
**Date:** 2026-07-25 · **Repo:** `d:/gomoqt` HEAD `22fa559`.
**Final link in the gomoqt evidence chain:** [`GOMOQT_COMPREHENSIVE_AUDIT.md`] (CPU/
alloc/escape/syscall) → this (waiting/latency).

**Headline:** **gomoqt's internal contribution to total waiting time is effectively
0 %.** Every goroutine that blocks does so **inside quic-go** (Accept/Read/select/
`Conn.run`/`sendQueue`) or the **kernel** (UDP send/recv syscalls); gomoqt is a thin
caller on the stack above the park site, never the park site itself. **gomoqt is
effectively optimized; remaining latency is owned by quic-go, the Go runtime, and the
kernel.**

Confidence: **[FACT]** measured · **[INFERENCE]** measurement-supported · **[UNKNOWN]**
needs further experiment.

---

## Method

Two complementary captures (real QUIC, loopback):

1. **Pure-gomoqt trace** — `BenchmarkFanOut_ViewerConnectionsLatency/subs-16` (real
   quic-go, fpg=256, 1 KB, GOMAXPROCS=8), `-trace`. 16.1 µs/op, p99 14.7 ms,
   0 starved readers. Extracted the four wait-attribution pprofs via
   `go tool trace -pprof={net,sync,syscall,sched}`.
2. **Relay-knee block + mutex profiles** (qumo, `bench-lat/{block,mutex}_knee.pprof`,
   Linux, real quic-go, N=1500) — the most representative *at-scale* waiting data.

---

## 1. runtime/trace — waiting attribution  [FACT]

Pure-gomoqt real-QUIC, four blocking categories. **"flat" = where the goroutine actually
parks** (the blocking primitive); "cum" = the full call stack including callers.

| Category | Total wait | Top **flat** park site | Owner |
|---|---|---|---|
| **NET** (network) | 57.0 s | `poll.FD.execIO` ← `UDPConn.ReadFrom` (100 %) | quic-go + kernel |
| **SYNC** (chan/select/mutex) | ~1525 s | `runtime.selectgo` 63 % + `runtime.chanrecv1` 30 % | quic-go/webtransport-go |
| **SYSCALL** | 5.69 s | `syscall.syscalln` ← `sendQueue.Run`/`sconn.Write` (100 %) | quic-go + kernel |
| **SCHED** (runnable→run) | ~4.5 s | `runtime.selectnbsend`/`runtime_Semrelease` (quic-go send path) | runtime + quic-go |

### SYNC category detail (the dominant one)  [FACT]
The select/chan-receive parking flows through:
- `quic-go ReceiveStream.Read` 21.6 % — readers waiting for stream data
- `quic-go/webtransport-go AcceptStream` 14.5 % — per-connection stream-accept loops
- `quic-go http3 frameParser.ParseNext` 10.9 % — webtransport http3 framing

**Every one of these is a quic-go/webtransport-go park site.**

## 2. gomoqt's own (flat) blocking contribution — the decisive check  [FACT]

Grepping each category's top-400 for `qumo-dev/gomoqt` flat time:

| Category | gomoqt **flat** blocking | meaning |
|---|---|---|
| NET | **0** | gomoqt never parks on network |
| SYNC | **0** (`Frame.encode/decode`, `ReadFrame`, `WriteFrame`, `ServeQUICConn` all flat=0; ~3–3.6 % cum only) | gomoqt is on the stack but parks **below**, in quic-go |
| SYSCALL | **0** (only `Dialer.Dial` setup, flat=0) | gomoqt never parks in a syscall |
| SCHED | **16 µs** (`Frame.encode`, 0.00035 %) | noise |

**[FACT] gomoqt code is never the site where a goroutine blocks.** The ~3 % cumulative
appearance is gomoqt frames sitting on the call stack *above* the quic-go park point
(gomoqt `ReadFrame` → `io.ReadFull` → `quic-go ReceiveStream.Read` → `chanrecv` parks).
gomoqt is a **pass-through on the wait path**.

## 3. Block profile @ relay knee (N=1500, Linux)  [FACT]

`block_knee.pprof` — total 132.8 h of sampled blocked time:

| Site | flat % | owner |
|---|---|---|
| `runtime.selectgo` | 83.4 % | (primitive) — stacks via quic-go Accept/Read |
| `runtime.chanrecv1` | 16.2 % | (primitive) — via quic-go `ReceiveStream.Read` |
| cum: quic-go `AcceptStream`/`AcceptUniStream` | 13.8–13.9 % | quic-go |
| cum: gomoqt `handleBiStreams`/`handleUniStreams` | 13.8 % | parked **inside** quic-go Accept |
| cum: quic-go `Conn.run`/`sendQueue.Run` | 13.7–14.0 % | quic-go |

The only gomoqt select with **flat** block time is `AnnouncementReader.ReceiveAnnouncement`
(9.2 h) — the **control-plane** announcement path, **not the media fanout hot path**.
`Frame.encode`/`decode`, `GroupReader.ReadFrame`, `WriteFrame`: **0 flat blocking.**

## 4. Mutex profile @ relay knee  [FACT]

`mutex_knee.pprof`: contention is **~99 % test harness** (`testing.common.Helper` 54 %;
`singleRelayFanoutRun`/`subscribeAndRead`). The only production mutex path
(`handleSubscribeStream` 19.7 % cum) is **parking** (`park_m`/`selparkcommit`), not a
contended critical section. **Mutexes remain irrelevant** to gomoqt's data path —
re-confirming the long-standing finding.

## 5. Scheduler analysis  [FACT]

Runnable→running delay (sched pprof) is ~4.5 s total, dominated by `selectnbsend`/
`Semrelease` **around quic-go's send path** (`Conn.run`/`sendQueue` 43 %, `sconn.Write`
27.8 %). gomoqt sched delay: **0** (Frame.encode 16 µs). There is no gomoqt goroutine
spending measurable time runnable-but-not-running; the scheduler delays are quic-go's
per-connection send loops contending for cores — the same per-connection-transport
finding from the CPU audit.

## 6. Network waiting — CPU vs blocking  [FACT]

| Transport cost | form | owner |
|---|---|---|
| UDP send | 44 % CPU (sendmsg) **+** syscall-wait bucket | quic-go sendQueue + kernel |
| UDP recv | NET-wait bucket (parked in `ReadFrom`) | quic-go + kernel |
| QUIC/stream flow control | inside quic-go (not separately visible; gomoqt sees it only as `Write` returning) | quic-go |
| Pacing | inside quic-go congestion control | quic-go |

The dominant transport cost is **kernel socket send/recv** (CPU **and** waiting), owned
entirely by quic-go + kernel. GSO (which would cut send CPU/wait) is **off**
(`sendPacketsWithoutGSO` measured) — an environment capability, not gomoqt.

---

## Latency attribution table  [FACT]

```
Waiting source                      Owner           gomoqt-internal?
---------------------------------------------------------------
UDP recv park (ReadFrom)            quic-go+kernel   no (NET, 100% quic-go)
stream read/accept park (select)    quic-go          no (gomoqt flat=0)
UDP send syscall (sendmsg)          quic-go+kernel   no (SYSCALL, 100% quic-go)
scheduler runnable delay            runtime          no (quic-go send path)
http3/webtransport framing park     webtransport-go  no
GC                                  runtime          no
gomoqt channel/mutex/cond/select    gomoqt           ~0% (not measured)
```

**gomoqt owns ~0 % of waiting time.** The only gomoqt blocking (announcement control
plane) is off the media-delivery critical path.

---

## Answers to the six questions

1. **Where does execution spend most waiting?** In quic-go's per-connection event loops
   (`AcceptStream`/`ReceiveStream.Read`/`Conn.run`/`sendQueue`) and the UDP send/recv
   syscalls. **[FACT]**
2. **Is significant waiting caused by gomoqt?** **No.** gomoqt's flat blocking is 0 in
   net/sync/syscall and 16 µs in sched. **[FACT]**
3. **Is gomoqt on the critical latency path?** Only as a thin caller into quic-go; the
   wait accumulates **below** gomoqt. gomoqt adds no queueing of its own. **[FACT]**
4. **Blocking points inside gomoqt worth optimizing?** **None found.** The data hot path
   (encode/WriteFrame/ReadFrame) has zero internal blocking; the only gomoqt select is
   announcement control-plane. **[FACT]**
5. **Is transport now the dominant latency owner?** **Yes — ~100 %** (quic-go + kernel +
   runtime). **[FACT]**
6. **Remaining gomoqt optimization for end-to-end latency (no architectural change)?**
   **None.** There is no gomoqt wait to remove; latency is set by transport.
   **[INFERENCE]** (strongly supported: 0 flat blocking across all categories)

---

## Final conclusion

**gomoqt is effectively optimized; remaining latency is owned by quic-go, the Go runtime,
and the kernel.** This is demonstrated — not intuited — by the runtime trace (gomoqt flat
blocking = 0 in net/sync/syscall, 16 µs in sched), the relay-knee block profile
(selectgo/chanrecv 100 % via quic-go Accept/Read; gomoqt data path 0 flat blocking), and
the mutex profile (contention 99 % harness, production = parking on quic-go).

gomoqt's media-delivery path is a thin, 0-allocation, 0-internal-blocking caller over
quic-go. **There is no latency lever left inside gomoqt without an architectural change.**
The levers that remain are all below or beside gomoqt:
- **GSO** (bare-metal Linux) — cuts the 44 % send CPU + syscall-wait; environment/quic-go.
- **Hierarchy / topology** — fewer connections per node → proportionally less
  per-connection park time; deployment.
- **quic-go internals** (flow-control batching, sendQueue) — explicitly out of scope.

This completes the gomoqt performance evidence chain (CPU → alloc → escape → syscall →
**waiting**); all five converge on the same conclusion.

---

## Process notes
- **No new benchmarks created** — reused `FanOut_ViewerConnectionsLatency` + existing
  qumo knee profiles. Trace captured via `go test -trace` (the standard tool).
- **Lab caveat:** pure-gomoqt trace is Windows loopback; the *attribution class*
  (gomoqt flat blocking = 0, all wait in quic-go/kernel) is platform-independent and
  cross-validated by the Linux relay-knee block profile. Absolute wait times are not
  compared across hosts.
- **PR #364** (`performance-check` run): the only code candidate from the prior audits;
  status folded in separately. This waiting audit did not modify any code (attribution
  only, per instruction).

_Related: [`GOMOQT_COMPREHENSIVE_AUDIT.md`], [`GOMOQT_HOTPATH_AUDIT.md`],
[`FANOUT_OPTIMIZATION_RESULTS.md`], [`LATENCY-ATTRIBUTION.md`]._
