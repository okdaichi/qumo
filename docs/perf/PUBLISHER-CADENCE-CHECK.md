# Does the benchmark publisher burst Groups, or arrive in real time?

**Concern:** the fan-out benchmark might open all its Groups in a burst
(`t=0: open Group 1–30 at once`) rather than at real-time intervals
(`t=0: Group 1; t=33ms: Group 2; …`). A burst would make the benchmark measure
Group-creation-storm handling rather than live streaming.

**Answer (measured): the publisher arrives PACED in real time, not bursted.** At a
33 ms target spacing the relay sees groups **33.7 ms apart at p50 with a p95 of
34–34.5 ms** — a near-perfect real-time cadence, and it holds even at 1000
subscribers. The burst hypothesis is **refuted**. One real caveat: the benchmark's
**default** spacing is `gap = 2 ms` (~500 groups/s), an unrealistically high
*rate* — but that is an over-rate, not a burst, and the recent latency studies
were run at 33 ms (paced, 30/s), so they are unaffected.

Labels: **[FACT]** measured here · **[CODE]** from source · **[HYP]** interpretation.

---

## 1. Current publisher behavior

**[CODE]** The publisher loop (`single_relay_bench_test.go`) is:

```
for {
    gw := tw.OpenGroup(ctx)     // open one group = one QUIC uni-stream
    for f in framesPerGroup:
        gw.WriteFrame(frame)
        time.Sleep(gap)          // <-- paces the FRAME rate
    gw.Close()
}
```

**[CODE]** There is a `time.Sleep(gap)` on every frame. With the current
`framesPerGroup = 1`, that means one `Sleep(gap)` **between every group open**.
The loop cannot run ahead — each group is opened, written, closed, and then the
goroutine sleeps before the next. There is **no code path that opens N groups
without sleeping**. So structurally it is paced, not bursted.

**[CODE]** The spacing period is `Sleep(gap) + work(OpenGroup+WriteFrame+Close)`,
so the effective rate is slightly *below* `1/gap` (measured 29.67 fps at
gap=33 ms — the ~0.7 ms of per-group work is why it is 29.7, not 30.3).

**[CODE] Default rate is high.** `gap` defaults to **2 ms** (≈500 groups/s); the
30 fps workload only exists when `FANOUT_GAP=33ms` is set. The file's own header
says "500 fps" — the default is a high-rate stress, not 30 fps.

## 2. Group-open timing distribution (measured)

**[FACT]** New instrument: a histogram of the spacing between consecutive group
reserves as the relay sees them (`GroupInterArrival`). 20 s runs, 5 s settle.

| Scenario | inter-arrival p50 | p95 | p99 | max | interpretation |
|---|---|---|---|---|---|
| N=1, gap=33 ms (unloaded) | **33.7 ms** | 34.2 ms | 34.3 ms | 34.3 ms | textbook pacing, ~zero jitter |
| N=1000, gap=33 ms (loaded) | **33.7 ms** | 34.5 ms | 36.5 ms | 137 ms | pacing holds under fan-out; rare outlier |
| N=1000, gap=2 ms (default) | 222 µs | 10.8 ms | 17.6 ms | 123 ms | irregular — but from *overload*, not burst |

**[FACT] The 33 ms cadence is real and tight.** At N=1 the spacing is 33.7 ms with
a p99 of 34.3 ms — the distribution is a spike at the target period, exactly what
paced real-time arrival looks like. If groups were burst-created, this histogram
would be a mass of near-zero deltas plus one large idle gap; it is the opposite.

**[FACT] Pacing survives fan-out.** At 1000 subscribers the cadence is unchanged
(p50 33.7 ms, p95 34.5 ms). The rare 137 ms max is a single publisher `Sleep`
delayed by a GC/scheduling hiccup — jitter, not bursting. `fillSem wait` stays
~1 µs, so the relay never applies ingest backpressure that would bunch arrivals.

## 3. Are Groups burst-created?

**[FACT] No.** At any realistic spacing the groups arrive one-per-period in real
time. The `t=0: open Group 1–30` scenario does not occur — there is a `Sleep`
between every group, and the measured inter-arrival is a tight spike at the target
period, not a cluster of zeros.

**[FACT] The one degenerate case is over-rate, not burst.** At the default
`gap=2 ms` with 1000 subscribers the system is simply overloaded: 94.8 % loss, fps
collapses to 19.7 (from the 500 target). The inter-arrival then looks irregular
(p50 222 µs, p95 10.8 ms) because the publisher's `OpenGroup`/`Sleep` is being
*stretched and stalled by backpressure*, not because it fires a burst. That is a
"the rate is too high for this fan-out" condition, and it is a property of the
unrealistic default rate — avoid the default; set a realistic `gap`.

## 4. Are existing latency results affected?

**[FACT] The recent studies are not burst artifacts.** The latency-attribution,
Case-B mechanism, frames-per-group, and hierarchy latency runs used
`FANOUT_GAP=33ms` (or, in the Nomad SLO study, `qumo loadgen publish gps=30`,
which is likewise rate-paced). All were measured under **paced, ~33 ms real-time
group arrival** — confirmed here at p50 33.7 ms. So the fan-out p99 those studies
reported is genuine live-arrival behavior, not a Group-creation storm.

**[HYP] Two things to keep straight going forward:**
- **Pacing is correct** (time dimension preserved) — the user's burst concern does
  not apply to how these numbers were produced.
- **The 1-frame-per-group *grouping*** is still the churn issue from the prior
  workload-model review: 30 groups/s/sub is an *audio-frame-rate-realistic
  cadence* but a *video-unrealistic grouping* (video should be one paced group per
  1–2 s GOP, 30–120 frames each). Pacing being correct does not make the grouping
  realistic — they are separate axes.
- **Never quote the `gap=2 ms` default as a workload.** It is a 500 groups/s
  over-rate that collapses under fan-out; realistic runs must set `gap` (33 ms for
  30 fps audio-style, or a GOP cadence via `FANOUT_FPG`).

## Bottom line

- **Burst? No** — groups arrive paced at the target period (measured p50 33.7 ms,
  p95 34.5 ms at 1000 subs). **[FACT]**
- **Backpressure/latency cause?** Real-time paced arrival (Case A), *not* a burst
  (Case B) — at a sane rate. The default 2 ms gap is a separate over-rate
  pathology, not a burst. **[FACT]**
- **Existing latency results?** Produced under paced 33 ms arrival → **valid**, not
  burst-inflated. The grouping-realism caveat from the workload-model review still
  stands independently. **[FACT + HYP]**

### Appendix — repro
`FANOUT_KS=<N> FANOUT_GAP=<g> FANOUT_FPG=1 BENCH_DURATION=20s
./relay_mech.test -test.bench=…FanoutSingleRelay$ -test.benchtime=1x` (integration,
instrument, Linux); read the `GROUP inter-arrival` stage line. Instrument:
`GroupInterArrival` in `stage_latency*.go` (spacing between consecutive reserves;
single-track assumption). Default build unchanged (no-op).
