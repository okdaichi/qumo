# @qumo/log

A small, fast logging library for **real-time media apps** — Media over QUIC, audio/video streaming,
WebCodecs pipelines. It speaks the media vocabulary natively (fps, bitrate, jitter, frame PTS,
decode queues) instead of forcing every app to rebuild the same ad-hoc stats plumbing.

- **Media-domain first.** Curated subsystem tags, typed meters (`fps`/`bitrate`/`rate`/`gauge`), and
  media-timestamp (PTS) stamping on every log line.
- **Hot-path safe.** Every log method is a single numeric level compare before anything is touched;
  `meter.mark()` / `gauge.sample()` are O(1) and allocate nothing — safe inside per-frame
  decode/encode loops.
- **Stack-agnostic.** No dependency on WebCodecs, WebTransport, MSE, WebRTC, or `@qumo/moq`. You
  feed it numbers; it produces the rates and summaries a media pipeline needs.
- **Zero runtime dependencies.** Usable as-is from Deno, Vite, Webpack, esbuild, or a plain
  `<script>`. Does not touch `import.meta.env`.

## Install

Deno / [jsr](https://jsr.io):

```ts
import { createMediaLogger, MediaTags, setLevel } from "jsr:@qumo/log@^0.1";
```

Node (via [jsr npm compatibility](https://jsr.io/docs/npm-compatibility)):

```sh
npx jsr add @qumo/log
```

## Quick start

```ts
import { createMediaLogger, MediaTags } from "@qumo/log";

// A video-decode logger bound to the stream's media clock (µs). Every entry it
// emits carries `mts` so logs align to the stream timeline, not wall-clock.
const log = createMediaLogger(MediaTags.video, { clock: () => mediaTimeUs });

log.info("decoder configured", { codec: "avc1.42E01E", width: 1280, height: 720 });
log.frame("info", "keyframe", { pts, type: "key" }); // pts becomes mts
log.error("decode failed", { err }); // Error survives exportLogs()
```

Output (via the built-in console sink):

```
[video] t=00:00:01.500 decoder configured { codec: 'avc1.42E01E', ... }
[video] t=00:00:02.000 keyframe { pts: 2000000, type: 'key' }
```

## Per-frame metrics (the meters)

Do **not** call `log.debug()` inside a decode/encode loop — JS evaluates the arguments before the
call, so even a suppressed log pays for them. Use the meters instead. `mark()`/`sample()` only bump
a number; a shared 1s timer flushes one readable summary line per active meter.

```ts
const log = createMediaLogger(MediaTags.video);

const fps = log.meter.fps("decode"); // → "decode: 30.0 fps (total 900)"
const br = log.meter.bitrate("egress"); // → "egress: 2.40 Mbps"  (mark with bytes)
const resets = log.meter.rate("subscribe resets"); // → "subscribe resets: 5.0/s (total 5)"
const jitter = log.meter.gauge("jitter", { unit: "ms" }); // → "jitter: avg 12.3ms (min 4ms, max 31ms)"

for await (const frame of group.frames()) {
	fps.mark();
	br.mark(frame.bytes);
	jitter.sample(rttMs);
}
```

| Meter                       | Call            | Flushes                                              |
| --------------------------- | --------------- | ---------------------------------------------------- |
| `meter.fps(name)`           | `mark([n])`     | `<name>: 30.0 fps (total 900)`                       |
| `meter.bitrate(name)`       | `mark(byteLen)` | `<name>: 2.40 Mbps` (auto-scales bps/kbps/Mbps/Gbps) |
| `meter.rate(name)`          | `mark([n])`     | `<name>: 5.0/s (total 5)`                            |
| `meter.gauge(name, {unit})` | `sample(value)` | `<name>: avg 12.3ms (min 4ms, max 31ms)`             |

All meters flush once per second on a single shared timer, regardless of how many you create.

## Levels & tags

Levels: `trace < debug < info < warn < error`. Default global level is `info`. Runtime control:

```ts
setLevel("debug"); // global
setLevel("trace", MediaTags.video); // one tag (per-tag override)
setLevel("warn"); // production: quiet everything below warn
getLevel(MediaTags.video);
```

Curated tags: `audio`, `video`, `encoder`, `decoder`, `renderer`, `capture`, `transport`, `network`,
`moq`, `quic`, `webtransport`, `jitter`, `catalog` (see `MediaTags`). Dotted children like
`"video.decode"` work and are independently tunable. Free-form strings are also accepted.

## Structured fields & media time

Pass an object as the second argument — values are kept as-is and only formatted at emit time, so
**suppressed logs pay nothing to build**:

```ts
log.info("frame", { seq, bytes, keyframe: true });
```

When a `clock` is bound to a media logger, every entry records `mts` (media timestamp, µs) alongside
the wall-clock `ts`. `frame(level, msg, { pts, ... })` uses the `pts` field as the media timestamp
directly — the accurate choice for frame events. Exports and the console render media time as a
stream timecode (`t=HH:MM:SS.mmm`).

## Noisy logs

Message-only repeats collapse into one `×N` entry automatically. For noisy _structured_ logs,
rate-limit:

```ts
log.throttle("warn", "jitter high", { ms: 52 }, 200); // ≤ once / 200ms; drops counted
```

## Ring buffer & export

The last 1024 entries are retained in a fixed ring buffer. Drain it for a bug report — calling this
never loses logs:

```ts
exportLogs(); // human-readable transcript (includes media timecodes)
exportLogs({ json: true }); // newline-delimited JSON
retainedLogCount();
```

`Error` field values are serialized (`name`/`message`/`stack`) so they survive the export (where
`JSON.stringify` would otherwise render `{}`).

Wire it into a "Copy logs" button or a dev-only console handle:

```ts
Object.assign(globalThis, { qumoLog: { setLevel, getLevel, exportLogs } });
```

## Sinks & live view

The built-in console sink is always installed. Register more (e.g. a batched HTTP shipper) with
`addSink(fn)`. A reactive debug UI subscribes without coupling to any framework:

```ts
addSink((entry) => ship(entry)); // your transport
const off = onLogs((entry) => append(entry)); // every emitted entry
const off2 = onLevelChange(() => refresh()); // level changes
```

## Generic engine (for non-media reuse)

`@qumo/log` is media-first, but the underlying engine is a fine generic logger too.
`createLogger(tag)` and `counter()` are exported and carry no media semantics — useful for mixed
apps or non-media code paths.

## API

| Export                                         | Kind            | Purpose                                                              |
| ---------------------------------------------- | --------------- | -------------------------------------------------------------------- |
| `createMediaLogger(tag, { clock? })`           | → `MediaLogger` | Media logger: levels + `frame()` + `meter` (fps/bitrate/rate/gauge). |
| `createLogger(tag)`                            | → `Logger`      | Generic tagged logger (levels + `throttle`/`counter`).               |
| `MediaTags`                                    | const           | Curated media subsystem tag strings.                                 |
| `formatBitrate(bps)` / `formatMediaTime(µs)`   | fn              | Unit helpers.                                                        |
| `setLevel(level, tag?)` / `getLevel(tag?)`     | fn              | Runtime level (global or per-tag).                                   |
| `exportLogs({ json? })` / `retainedLogCount()` | fn              | Drain the ring buffer.                                               |
| `addSink(fn)` / `removeSink(fn)`               | fn              | Register/remove an output sink.                                      |
| `addFlush(fn)`                                 | fn              | Hook the shared 1s pulse (custom aggregators).                       |
| `onLogs(fn)` / `onLevelChange(fn)`             | fn              | Subscribe; return unsubscribe.                                       |

## Performance notes

- Suppressed log = one numeric compare + return. Set the level to `warn` (or higher) in production.
- Call-site arguments are still evaluated before the call, so keep `msg`/`fields` cheap; use
  `meter.*()` for genuinely high-rate data.
- The ring buffer is preallocated and reused — no growing array, no per-entry GC pressure beyond the
  single entry object per emitted log.
- One flush timer for the whole library, no matter how many counters/meters exist.

## License

MIT
