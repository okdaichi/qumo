# @okdaichi/log

A small, fast, framework-agnostic logging library for the browser (and Deno). Tagged,
level-filtered, structured, with a ring buffer of recent entries for bug-report export,
dedup/rate-limiting for noisy logs, and counter aggregation for the high-frequency paths real-time
media pipelines (Media over QUIC, audio/ video) hit every frame.

- **Zero runtime dependencies.** Usable as-is from Deno, Vite, Webpack, esbuild, or a plain
  `<script>`. It does not touch `import.meta.env`.
- **Hot-path safe.** Every log method is a single numeric level compare before anything is touched;
  a suppressed log is near-free.
- **Framework-agnostic.** No Solid/React/Vue coupling; a reactive debug UI can subscribe via
  `onLogs()` / `onLevelChange()`.

## Install

Deno / [jsr](https://jsr.io):

```ts
import { createLogger, exportLogs, setLevel } from "jsr:@okdaichi/log@^0.1";
```

Node (via [jsr npm compatibility](https://jsr.io/docs/npm-compatibility)):

```sh
npx jsr add @okdaichi/log
```

## Quick start

```ts
import { createLogger, exportLogs, setLevel } from "@okdaichi/log";

const log = createLogger("subscribe.video");

log.info("decoder configured", { codec: "avc1.42E01E", width: 1280, height: 720 });
log.warn("catalog acceptGroup failed", { err: groupErr });
log.error("decode failed", { err }); // Error fields survive in exportLogs()
```

Output (via the built-in console sink):

```
[subscribe.video] decoder configured { codec: 'avc1.42E01E', width: 1280, height: 720 }
[subscribe.video] catalog acceptGroup failed { err: ... }
```

## Levels & tags

Levels: `trace < debug < info < warn < error`. The default global level is `info`. The level is a
runtime control:

```ts
setLevel("debug"); // global
setLevel("trace", "subscribe.video"); // one tag only (per-tag override)
setLevel("warn"); // production: quiet everything below warn
getLevel("subscribe.video");
```

Tags are free-form strings; dotted tags (`"subscribe.video"`) read naturally and can be tuned
independently. Create one logger per module/subsystem.

## Structured fields

Pass an object as the second argument — values are kept as-is and only formatted by a sink at emit
time, so **suppressed logs pay nothing to build**:

```ts
log.info("frame", { seq, bytes, keyframe: true });
```

## Noisy logs

Message-only repeats collapse into one `×N` entry automatically. For noisy _structured_ logs,
rate-limit:

```ts
// At most once per 200ms; buffered drops reported on the next emit.
log.throttle("warn", "jitter high", { ms: 52 }, 200);
```

## High-frequency paths (per-frame)

Do **not** call `log.debug()` inside a decode/encode loop — JS evaluates the arguments before the
call, so even a suppressed log pays for them. Use a counter: `mark()` is O(1) and allocates nothing;
a shared 1s timer flushes one summary line per active counter.

```ts
const dropped = log.counter("dropped frames");
for await (const frame of group.frames()) {
	// ...
	if (stalled) dropped.mark();
}
// → [subscribe.video] dropped frames: 12/s (total 348)
```

## Ring buffer & export

The last 1024 entries are retained in a fixed ring buffer. Drain it to text (or ndjson) for a bug
report — calling this never loses logs:

```ts
exportLogs(); // human-readable transcript
exportLogs({ json: true }); // newline-delimited JSON
retainedLogCount();
```

`Error` field values are serialized (`name`/`message`/`stack`) so they survive the text export
(which uses `JSON.stringify`, where `Error` would otherwise render as `{}`).

Wire it into a "Copy logs" button or a dev-only console handle:

```ts
// dev-only
Object.assign(globalThis, {
	qumoLogs: { setLevel, getLevel, exportLogs },
});
```

## Sinks

The built-in console sink is always installed. Register more (e.g. a batched HTTP shipper) with
`addSink(fn)`; remove with `removeSink(fn)`.

```ts
addSink((entry) => ship(entry)); // your transport
```

## Live view (for a debug UI)

A reactive UI subscribes without coupling to any framework:

```ts
const off = onLogs((entry) => append(entry)); // every emitted entry
const off2 = onLevelChange(() => refresh()); // level changes
```

## API

| Export                             | Kind       | Purpose                                                                |
| ---------------------------------- | ---------- | ---------------------------------------------------------------------- |
| `createLogger(tag)`                | → `Logger` | Tagged logger with `trace/debug/info/warn/error/log/throttle/counter`. |
| `setLevel(level, tag?)`            | fn         | Runtime level (global or per-tag).                                     |
| `getLevel(tag?)`                   | fn         | Effective level for a tag.                                             |
| `exportLogs({ json? })`            | fn         | Drain the ring buffer to a transcript.                                 |
| `retainedLogCount()`               | fn         | Entries currently held.                                                |
| `addSink(fn)` / `removeSink(fn)`   | fn         | Register/remove an output sink.                                        |
| `onLogs(fn)` / `onLevelChange(fn)` | fn         | Subscribe; return unsubscribe.                                         |

## Performance notes

- Suppressed log = one numeric compare + return. Set the level to `warn` (or higher) in production.
- Call-site arguments are still evaluated before the call, so keep `msg`/`fields` cheap; use
  `counter()`/`throttle()` for genuinely high-rate data.
- The ring buffer is preallocated and reused — no growing array, no per-entry GC pressure beyond the
  single entry object per emitted log.

## License

MIT
