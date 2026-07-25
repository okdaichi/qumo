# Relay benchmark workload model: is 1-frame-per-group realistic?

**Question:** the fan-out benchmark publishes 30 fps with **1 frame = 1 group**
(30 group-opens/s/subscriber → 30 000/s at 1000 subs). Does this represent a
realistic MoQ media workload, or an artificial Group-lifecycle stress test — and
is the high fan-out latency a *"too many subscribers"* problem or a *"too many
Group operations per second"* problem?

**Answer (measured):** 1-frame-per-group is **not** a realistic video workload —
it is the group-churn extreme. Real video groups a GOP (~30–120 frames) onto one
stream. And the current benchmark's alarming p99 is **mostly the churn artifact**:
at the same 1000 subscribers and same 30 fps, switching to a realistic 1–2 s GOP
**cuts p99 from 233 ms to ~42–48 ms (~5×) and loss from 8.8 % to 0 %**. A real
per-subscriber scheduler-contention floor remains (~11 ms p50), but the tail that
made fan-out look catastrophic is group-lifecycle churn, not subscriber count.

Labels below: **[FACT]** measured here · **[SPEC]** external MoQ/media protocol ·
**[HYP]** hypothesis/interpretation.

---

## 1. Current benchmark assumption analysis

**[FACT]** The publisher loop is `OpenGroup → WriteFrame(1) → Close`, once per
frame (`single_relay_bench_test.go`). Each group is one QUIC uni-stream opened,
written once, and closed. At 30 fps that is **30 groups/s/subscriber = 30 000
stream opens/s at 1000 subs**, confirmed by the O-stage counter (403 011 opens in
15 s ≈ 26.9 K/s).

**[FACT]** The relay's own design already assumes multi-frame groups:
`MaxFramesPerGroup = 256`, commented *"256 leaves headroom over a typical
~120-frame (60 fps × 2 s) video group."* The group cache, the batched egress
counter (#333, "O(frames) → O(groups)"), and the trickle-wait in `deliverGroup`
are all built for groups that carry **many** frames on one long-lived stream. The
benchmark contradicts the code's own stated model.

**[FACT/interpretation]** So the benchmark is a **Group-lifecycle stress test**:
it maximizes stream open/close churn (the most expensive per-group operation) by
making every frame its own group. That is a legitimate upper-bound probe for
stream-lifecycle overhead — but it is not steady media delivery.

## 2. Realistic video Group/Frame model

**[SPEC]** MoQ Transport hierarchy: **Track → Group → (Subgroup) → Object**. A
**Group** is the largest independently-joinable unit — *typically a closed GOP or
a CMAF segment*, beginning with a keyframe. A **Subgroup maps to exactly one QUIC
stream**; its Objects (one per encoded frame) are written to that stream in order.
gomoqt implements this directly: `OpenGroup` opens the stream, `WriteFrame` writes
an Object, `Close` ends it. (Sources: IETF `draft-ietf-moq-transport`; MoQ
streaming-format practice — see references.)

**[SPEC]** Typical values:

| Parameter | Typical | Notes |
|---|---|---|
| Group = | 1 GOP / CMAF segment | keyframe boundary |
| Group duration | **1–2 s** (LL up to ~0.5 s) | encoder keyframe interval |
| Frames per group | **30–120** (30–60 fps × 1–2 s) | e.g. "chunk 0 keyframe + chunks 1–99 P/B frames" |
| Group-open rate | **0.5–1 /s/subscriber** | = 1 / GOP-duration |
| Frame size | **highly skewed**: keyframe 10–50 KB, delta 0.5–3 KB | current model's flat 1200 B misses the keyframe spike |

**[FACT] Verdict: 1-frame-per-group is *not* realistic for video.** Real video is
**30–120× fewer group opens** than the current benchmark for the same frame rate.
The user's expectation (1–2 s groups, 30–60 frames/group) is correct.

## 3. Realistic audio Group/Frame model

**[SPEC]** Audio differs fundamentally: **every audio frame is independently
decodable** (no GOP / keyframe), so the group boundary is a packaging choice, not
a codec constraint.

| Parameter | Opus | AAC-LC |
|---|---|---|
| Frame duration | 20 ms (10/20/40/60 configurable) | 1024 samples → ~21.3 ms @48 kHz |
| Frame rate | **~50 /s** | **~47 /s** |
| Frame size | ~40–400 B (16–128 kbps) | ~100–500 B |

**[SPEC]** Grouping practice for audio varies more than video:
- **Frame-per-group is more defensible for audio than video** (independent
  decodability, fine drop granularity for real-time) — some low-latency designs
  do use 1 object = 1 group.
- **But batching is common for efficiency**: a group per ~1 s (≈50 objects on one
  stream), or one long-lived subgroup stream carrying the whole session's objects.

**[FACT/interpretation]** The current benchmark (30 fps, 1200 B, 1-frame-group)
matches **neither** cleanly: video frame rate but audio-style per-frame grouping
and mid-size frames. It is a synthetic point, not audio or video. Audio, if
modeled, should be ~50 fps, ~160 B frames, and its own grouping sweep (per-frame
vs ~1 s batches).

## 4. Measured impact on current conclusions

**[FACT]** Frames-per-group sweep, N=1000, 30 fps, 20 s (5 s settle), instrument
build. Only the group/frame ratio changes; frame rate is identical.

| Frames/group | GOP | group-opens/s | **e2e p99** | e2e p50 | loss | R.wake p50 (group-pickup) | C write-frame p50 |
|---|---|---|---|---|---|---|---|
| **1** (current) | per-frame | ~30 000 | **232.9 ms** | 14.8 ms | 8.8 % | 4.2 ms | 1 µs |
| **30** | 1 s | ~1 000 | **41.7 ms** | 11.5 ms | 2.5 % | 5.7 ms | 4 µs |
| **60** | 2 s | ~500 | **47.7 ms** | 11.5 ms | 0 % | 5.2 ms | 4 µs |

(Frame delivery count is constant ~30 K/s in all three — C-stage n ≈ 434 K–445 K
over 15 s. Only group opens change.)

**[FACT] The tail is churn, the floor is fan-out.** Realistic GOP grouping cuts
p99 **~5×** (233 → ~45 ms) and eliminates loss, at identical subscriber count and
frame rate. But e2e **p50 barely moves** (14.8 → 11.5 ms) and R.wake p50 stays
~5 ms — the per-frame broadcast→schedule floor (the Case-B mechanism, see
`FANOUT-MECHANISM.md`) is real and workload-independent.

**[HYP] Why:** at 1-frame-group, every frame's pickup runs a full stream open
(`OpenGroupAt`: uni-stream create + MAX_STREAMS accounting + header writes, which
can *block*). 30 K/s of that across 1000 subscribers is what inflates the tail and
drives loss. At GOP grouping, the stream opens once per 1–2 s and the other 29–59
frames are just `WriteFrame` on the already-open stream (4 µs) — the heavy
per-frame work disappears, so the herd drains far faster and the tail collapses.
The ~11 ms p50 that remains is the per-frame broadcast wakeup of 1000 goroutines
onto the pinned cores, which grouping does **not** change (broadcast is per-frame).

**Re-framing the critical question — it is *both*, decomposed:**
- The **p99 tail** in the current benchmark (the scary 233 ms) is **primarily a
  Group-lifecycle-ops/sec artifact** (30 K stream opens/s). *"The relay cannot
  handle an unrealistic number of Group operations per second"* — **[FACT]**, it
  drops ~5× when the op rate becomes realistic.
- The **p50 floor** (~11 ms) is a **real many-subscribers cost** — the per-frame
  broadcast herd → scheduler run-queue — and persists under realistic grouping.

**[FACT] Consequence for prior findings:**
- The *"OpenGroupAt is ~61 % of deliverGroup CPU"* attribution (PR #348) and the
  gomoqt stream-open micro-optimizations (openTimeout, coalesced-write) target a
  cost paid **30–60× less often** in realistic video. They are correct but their
  real-world weight is proportionally smaller than the 1-frame-group benchmark
  implied. (openTimeout was already established as efficiency-only, not latency —
  this reinforces it.)
- The hierarchy latency win (227 → 34 ms with 8 edges) was measured at
  1-frame-group; **[HYP]** under realistic GOP grouping the flat-topology p99 is
  already ~45 ms, so hierarchy's *relative* latency advantage is likely smaller
  than the 1-frame-group numbers suggest — worth re-measuring per Profile A.
- The single-relay capacity/SLO numbers (~1000–1500 subs under a 300 ms p99 SLO)
  used 1-frame-group; **[HYP]** realistic grouping (lower p99, zero loss) likely
  **raises** the SLO-bound subscriber count — the SLO study should be re-run on
  Profile A before any capacity claim is quoted for "video."

## 5. Proposed benchmark matrix

Built (uncommitted): the publisher now takes `FANOUT_FPG` (frames per group);
`FANOUT_FPG=1` is the current behavior. Proposed named profiles:

| Profile | Purpose | fps | frames/group | frame size | group-opens/s @1000 |
|---|---|---|---|---|---|
| **A1 video 1 s GOP** | realistic live video | 30 | 30 | keyframe 20 KB + delta 1.2 KB | ~1 000 |
| **A2 video 2 s GOP** | realistic live video | 30 | 60 | keyframe 30 KB + delta 1.2 KB | ~500 |
| **B audio** | realistic audio | 50 | 50 (1 s) *and* 1 (per-frame) | ~160 B | ~1 000 / ~50 000 |
| **C churn stress** | **renamed** upper-bound | 30 | **1** | 1200 B | ~30 000 |

Rules: **C is explicitly labeled "maximum Group-lifecycle stress test" and is
never the headline capacity/latency number.** A1/A2 are the default for any
"video capacity/latency" statement; B for audio. Each profile reports the same
metrics (max subs, p50/p95/p99, CPU, memory, group-opens/s, loss) via the
existing loadgen latency histogram + stage report — no new measurement mechanism.

**[HYP] One gap to add for full realism:** a keyframe-size spike (variable frame
size within a group) — the flat 1200 B model misses the periodic large-I-frame
burst delivered to all N subscribers every GOP, which is a distinct bandwidth/CPU
stress the current model cannot show. Proposed but **not yet implemented**.

## Remaining uncertainty
- `deliverSpan` reads the 1 s histogram ceiling at FPG≥30 (correctly — a
  subscriber's `deliverGroup` now spans the whole GOP), and the
  `maxConcurrentDeliveries` gauge is unreliable at FPG≥30 (known accounting race,
  see `FANOUT-MECHANISM.md`); the conclusion rests on O-stage counts, e2e p99, and
  loss, which are solid.
- Single-host WSL2 caveats from prior reports still apply (co-located loadgen,
  4-core pin); absolute magnitudes are environment-specific, cross-FPG comparison
  under identical conditions is the valid result.
- Frame-size realism (keyframe spikes) is not yet modeled — the p99/loss
  improvements above may be *optimistic* vs a real keyframe-burst workload.

## References
- [draft-ietf-moq-transport (IETF)](https://moq-wg.github.io/moq-transport/draft-ietf-moq-transport.html)
- [MoQ use cases (IETF)](https://www.ietf.org/archive/id/draft-lcurley-moq-use-cases-00.html)
- [Cloudflare: MoQ — refactoring the real-time media stack](https://blog.cloudflare.com/moq/)
- In-repo: `internal/relay/group_cache.go` (`MaxFramesPerGroup`), `docs/perf/FANOUT-MECHANISM.md`
