# Workload model

Why the audio baseline uses **one frame per group**, how that choice affects the
results, and how it differs from a realistic video workload. The relay forwards
groups exactly as the publisher sends them, so the group/frame ratio is a
property of the workload, not of the relay — and it changes the numbers
dramatically.

## The MoQ object model

Media over QUIC organizes data as **Track → Group → (Subgroup) → Frame**. In a
relay, the relevant mapping to QUIC is:

- A **group** corresponds to a GOP or CMAF segment.
- A **subgroup** is one QUIC unidirectional stream.
- `OpenGroup` opens a stream, `WriteFrame` writes an object to it, `Close` closes
  the stream.

So the **group-open rate equals the stream-open rate**. With one frame per group,
publishing 30 frames/s opens 30 new QUIC streams per second. With a 30-frame GOP,
it opens ~1 stream per second.

## Why one frame per group is a stress test

Realistic video groups 30–120 frames per stream (a 1–4 s GOP), so a subscriber
sees roughly 0.5–1 group-open per second. The benchmark's one-frame-per-group
model opens **30 streams per second per subscriber** — a deliberate group-churn
stress test that maximizes stream lifecycle work.

This is intentional for the audio baseline, because:

- Realtime audio frames are small and frequent; one object per group models the
  worst case for stream churn.
- It stresses the exact path (`OpenGroupAt` → stream open → `WriteFrame` →
  close) that a video GOP workload amortizes.

It is **not** a realistic video workload, and the two must not be mixed when
quoting capacity.

## What grouping does to the numbers

Sweeping frames-per-group at 1000 subscribers, 30 fps, 1200 B:

| frames/group | group-open rate | p99 | loss % |
|---|---|---|---|
| 1 (audio baseline) | 30/s | ~233 ms | ~8.8 |
| 30 (1 s GOP) | 1/s | ~45 ms | ~0 |
| 60 (2 s GOP) | 0.5/s | ~45 ms | ~0 |

Grouping 30 frames per group cuts p99 roughly **5×** and drives loss to zero at
the same subscriber count — purely by amortizing the per-group stream-open cost.
The p50 floor (~11 ms) is the real fan-out cost; the audio-baseline tail is
largely a churn artifact layered on top.

> **Note:** This is a measurement, not a recommendation to change the relay. The
> relay forwards groups as-is and cannot re-group a publisher's stream. For an
> audio publisher that emits one frame per group, the churn cost is real and is
> what the baseline reports. A video publisher that already groups into GOPs
> sees the lower numbers above.

## Publisher pacing

A separate check confirmed the benchmark publisher is **paced, not bursted**.
Group inter-arrival at the default 33 ms gap is p50 = 33.7 ms — groups open at
real-time intervals, not in a burst. (An earlier 2 ms default gap was 500/s,
over-rate; the audio baseline pins it to 33 ms for a true 30 fps.)

This matters because a burst publisher would open many streams at once and
inflate the fan-out drain artificially. The measured pacing confirms the knee is
the steady-state fan-out limit, not a burst artifact.

## See also

- [Baseline](baseline.md) — the one-frame-per-group numbers.
- [Bottleneck attribution](bottleneck-attribution.md) — why group-open cost
  shows up where it does.
