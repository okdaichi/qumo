// @qumo/log — a small, fast logging engine for real-time media apps (Media over
// QUIC, audio/video streaming), usable in the browser and Deno.
//
// This module is the generic engine: levels, tags, a ring buffer, dedup,
// throttle, sinks, export, and a shared "pulse" that periodic aggregators
// (generic counters and the media meters in media.ts) hook into so a single 1s
// timer flushes every summary line. The media-domain API lives in media.ts.
//
// Hot-path contract — read this before logging from a tight loop:
//   - Every public method reduces to a single numeric level compare before any
//     argument's *contents* are touched. When the level is suppressed the cost
//     is that compare plus a return: no closure allocation, no object spread, no
//     formatting. Setting the level to "warn" in production makes every
//     trace/debug/info call a near-no-op.
//   - NOTE: JavaScript evaluates a call's arguments *before* the call itself, so
//     a suppressed log still pays to evaluate its `msg` and `fields` expressions.
//     Keep those cheap (string literals, small field objects). For genuinely
//     high-rate data — e.g. per-video-frame diagnostics — use the meters in
//     media.ts (mark()/sample() only bump a number), or counter()/throttle().
//
// Zero runtime dependencies; does not touch `import.meta.env`. Production
// quietness is a runtime level concern (setLevel); build-time dead-code
// elimination of call sites, if desired, is the consumer's bundler choice.

/** Structured fields attached to a log entry. Values are kept as-is and only
 *  formatted by a sink at emit time, so suppressed logs pay nothing to build. */
export type Fields = Record<string, unknown>;

const NUM_LEVEL = {
	trace: 0,
	debug: 1,
	info: 2,
	warn: 3,
	error: 4,
} as const;

export type LogLevel = keyof typeof NUM_LEVEL;
type LevelNum = (typeof NUM_LEVEL)[LogLevel];

const LEVEL_NAME: readonly LogLevel[] = ["trace", "debug", "info", "warn", "error"];

/** A single buffered/observed log entry.
 *
 *  - `count` is how many consecutive identical (level,tag,msg) emits this record
 *    represents after dedup.
 *  - `mts` is an optional media timestamp in microseconds (e.g. a frame PTS),
 *    stamped by the media logger so logs align to the stream timeline rather
 *    than wall-clock. Omitted by the generic logger. */
export interface LogEntry {
	readonly ts: number;
	readonly mts?: number;
	readonly level: LogLevel;
	readonly levelNum: LevelNum;
	readonly tag: string;
	readonly msg: string;
	readonly fields?: Fields;
	count: number;
}

/** Output target — a bare function. The built-in console sink is always
 *  installed; register more (e.g. a batched HTTP shipper) via addSink(). */
export type Sink = (entry: LogEntry) => void;

const RING_SIZE = 1024;
const ring: LogEntry[] = [];
let ringHead = 0; // next write index; once full, overwrites the oldest entry
let totalWritten = 0;

// Last emitted entry, for consecutive-run dedup of message-only logs.
let lastEntry: LogEntry | null = null;

// --- level store ---------------------------------------------------------

let globalThreshold: LevelNum = NUM_LEVEL.info;
const tagThreshold = new Map<string, LevelNum>();
const levelListeners = new Set<() => void>();

function thresholdFor(tag: string): LevelNum {
	return tagThreshold.get(tag) ?? globalThreshold;
}

/** Set the runtime level — globally, or for a single tag.
 *  Per-tag overrides win over the global threshold. Passing no tag resets any
 *  per-tag overrides when changing the global level. */
export function setLevel(level: LogLevel, tag?: string): void {
	const n = NUM_LEVEL[level];
	if (tag) {
		tagThreshold.set(tag, n);
	} else {
		globalThreshold = n;
		tagThreshold.clear();
	}
	for (const fn of levelListeners) fn();
}

/** Effective level for a tag (per-tag override if set, else global). */
export function getLevel(tag?: string): LogLevel {
	const n = tag ? (tagThreshold.get(tag) ?? globalThreshold) : globalThreshold;
	return LEVEL_NAME[n];
}

/** Subscribe to level changes (e.g. for a reactive debug UI). Returns an
 *  unsubscribe function. */
export function onLevelChange(fn: () => void): () => void {
	levelListeners.add(fn);
	return () => {
		levelListeners.delete(fn);
	};
}

// --- sinks ---------------------------------------------------------------

const sinks: Sink[] = [consoleSink];

/** Register an additional output sink (e.g. a batched HTTP shipper). */
export function addSink(sink: Sink): void {
	sinks.push(sink);
}

/** Remove a previously-registered sink. */
export function removeSink(sink: Sink): void {
	const i = sinks.indexOf(sink);
	if (i >= 0) sinks.splice(i, 1);
}

// --- entry subscribers (live view, tests) --------------------------------

const entrySubs = new Set<(entry: LogEntry) => void>();

/** Observe every emitted entry in real time. The entry object is reused by the
 *  ring buffer — read it synchronously, don't retain it. Returns unsubscribe. */
export function onLogs(fn: (entry: LogEntry) => void): () => void {
	entrySubs.add(fn);
	return () => {
		entrySubs.delete(fn);
	};
}

// --- core ----------------------------------------------------------------

function pushRing(entry: LogEntry): void {
	if (ring.length < RING_SIZE) {
		ring.push(entry);
	} else {
		ring[ringHead] = entry;
	}
	ringHead = (ringHead + 1) % RING_SIZE;
	totalWritten++;
}

/** Emit a log entry directly (used by the media layer and wrappers). Returns
 *  nothing. Respects the level gate, dedup, sinks, ring buffer, and
 *  subscribers exactly like a Logger method. */
export function emit(
	level: LogLevel,
	tag: string,
	msg: string,
	fields?: Fields,
	mts?: number,
): void {
	coreLog(level, tag, msg, fields, mts);
}

function coreLog(
	level: LogLevel,
	tag: string,
	msg: string,
	fields?: Fields,
	mts?: number,
): void {
	const levelNum = NUM_LEVEL[level];
	if (levelNum < thresholdFor(tag)) return;

	// Dedup consecutive identical message-only emits: bump the last record's
	// count instead of pushing a new line. Structured logs (with fields) always
	// emit — they carry per-occurrence detail worth seeing; throttle() handles
	// noisy structured logs. Media-timestamped entries always emit too (their mts
	// is per-occurrence and meaningful).
	if (!fields && mts === undefined && lastEntry !== null) {
		const l = lastEntry;
		if (
			l.levelNum === levelNum && l.tag === tag && l.msg === msg && !l.fields &&
			l.mts === undefined
		) {
			l.count++;
			return;
		}
	}

	const entry: LogEntry = {
		ts: Date.now(),
		...(mts !== undefined ? { mts } : {}),
		level,
		levelNum,
		tag,
		msg,
		...(fields ? { fields } : {}),
		count: 1,
	};
	pushRing(entry);
	lastEntry = entry;

	for (const fn of entrySubs) fn(entry);
	for (const s of sinks) s(entry);
}

// --- pulse: shared 1s flush for periodic aggregators ---------------------
//
// Generic counters (counter()) and the media meters (media.ts) both need a
// periodic flush. They register a hook here; a single lazily-started 1s timer
// drives all of them, so there is ever only one timer regardless of how many
// call sites aggregate.

const PULSE_MS = 1000;
const flushHooks = new Set<() => void>();
let pulseTimer: ReturnType<typeof setInterval> | undefined;

// setInterval keeps the host alive. In a browser tab that's fine; in Deno
// (tests/SSR) we unref it so the library can't hang process exit. Browsers have
// no unref concept and the page owns the lifetime, so the call is a no-op there.
function setUnrefInterval(fn: () => void, ms: number): ReturnType<typeof setInterval> {
	const id = setInterval(fn, ms);
	const g = globalThis as { Deno?: { unrefTimer?: (id: unknown) => void } };
	g.Deno?.unrefTimer?.(id);
	return id;
}

/** Register a hook to be called once per second (on the shared pulse timer).
 *  The first registration starts the timer. Returns an unsubscribe function. */
export function addFlush(hook: () => void): () => void {
	flushHooks.add(hook);
	if (!pulseTimer) {
		pulseTimer = setUnrefInterval(() => {
			for (const h of flushHooks) h();
		}, PULSE_MS);
	}
	return () => {
		flushHooks.delete(hook);
	};
}

// --- counter (generic aggregation) ---------------------------------------
//
// mark() only bumps a number; the shared pulse flushes one summary line per
// active counter. For media-domain rates (fps/bitrate) and gauges, prefer the
// meters in media.ts.

export interface Counter {
	/** Bump the counter by n (default 1). Allocation-free, safe on the hot path. */
	mark(n?: number): void;
}

interface CounterState {
	readonly tag: string;
	readonly name: string;
	delta: number;
	total: number;
}

const counters: CounterState[] = [];
let counterHookRegistered = false;

function ensureCounterHook(): void {
	if (counterHookRegistered) return;
	counterHookRegistered = true;
	addFlush(() => {
		for (const c of counters) {
			const delta = c.delta;
			if (delta === 0) continue;
			c.delta = 0;
			emit("info", c.tag, `${c.name}: ${delta}/s (total ${c.total})`);
		}
	});
}

// --- logger façade -------------------------------------------------------

export interface Logger {
	trace(msg: string, fields?: Fields): void;
	debug(msg: string, fields?: Fields): void;
	info(msg: string, fields?: Fields): void;
	warn(msg: string, fields?: Fields): void;
	error(msg: string, fields?: Fields): void;
	/** Explicit level (used by wrappers/tests). */
	log(level: LogLevel, msg: string, fields?: Fields): void;
	/** Emit at most once per windowMs; subsequent calls within the window are
	 *  dropped and counted, with the drop total folded into the next emit.
	 *  Keyed by msg within this logger. Use for noisy structured logs. */
	throttle(level: LogLevel, msg: string, fields: Fields | undefined, windowMs: number): void;
	/** Register a named counter — a generic events/sec aggregator. mark() is O(1)
	 *  and allocates nothing. For fps/bitrate/jitter, prefer the media meters. */
	counter(name: string): Counter;
}

/** Create a tagged logger. `tag` is the category — e.g. "subscribe",
 *  "subscribe.video", "transport". Dotted tags read naturally in the console
 *  and in exported logs, and can be level-tuned independently via setLevel. */
export function createLogger(tag: string): Logger {
	// throttle state, keyed by msg within this logger
	const throttleState = new Map<string, { until: number; dropped: number }>();

	return {
		trace: (msg, fields) => coreLog("trace", tag, msg, fields),
		debug: (msg, fields) => coreLog("debug", tag, msg, fields),
		info: (msg, fields) => coreLog("info", tag, msg, fields),
		warn: (msg, fields) => coreLog("warn", tag, msg, fields),
		error: (msg, fields) => coreLog("error", tag, msg, fields),
		log: (level, msg, fields) => coreLog(level, tag, msg, fields),
		throttle: (level, msg, fields, windowMs) => {
			const now = Date.now();
			const st = throttleState.get(msg);
			if (st && now < st.until) {
				st.dropped++;
				return;
			}
			const dropped = st?.dropped ?? 0;
			const entryFields: Fields = { ...fields };
			if (dropped > 0) entryFields.dropped = dropped;
			coreLog(level, tag, msg, Object.keys(entryFields).length ? entryFields : undefined);
			throttleState.set(msg, { until: now + windowMs, dropped: 0 });
		},
		counter: (name) => {
			let state = counters.find((c) => c.tag === tag && c.name === name);
			if (!state) {
				state = { tag, name, delta: 0, total: 0 };
				counters.push(state);
				ensureCounterHook();
			}
			return {
				mark(n = 1) {
					state!.delta += n;
					state!.total += n;
				},
			};
		},
	};
}

// --- export (bug reports) ------------------------------------------------

function pad2(n: number): string {
	return n < 10 ? `0${n}` : String(n);
}

function pad3(n: number): string {
	return n < 10 ? `00${n}` : n < 100 ? `0${n}` : String(n);
}

function formatTs(ts: number): string {
	const d = new Date(ts);
	return (
		`${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}` +
		"." + pad3(d.getMilliseconds())
	);
}

/** Format a media timestamp (microseconds) as a stream timecode HH:MM:SS.mmm. */
export function formatMediaTime(mts: number): string {
	const totalSec = Math.floor(mts / 1_000_000);
	const h = Math.floor(totalSec / 3600);
	const m = Math.floor((totalSec % 3600) / 60);
	const s = totalSec % 60;
	const ms = Math.floor((mts / 1000) % 1000);
	return `${pad2(h)}:${pad2(m)}:${pad2(s)}.${pad3(ms)}`;
}

// JSON.stringify replacer that surfaces Error fields (name/message/stack), which
// are non-enumerable and would otherwise serialize as {} — unacceptable for a
// bug-report export where the error is usually the point. Runs only at export.
function errorReplacer(_key: string, value: unknown): unknown {
	if (value instanceof Error) {
		return { name: value.name, message: value.message, stack: value.stack };
	}
	return value;
}

function formatEntry(e: LogEntry): string {
	const count = e.count > 1 ? ` ×${e.count}` : "";
	const mtime = e.mts !== undefined ? ` t=${formatMediaTime(e.mts)}` : "";
	const head = `${formatTs(e.ts)} [${e.level.toUpperCase()}] [${e.tag}]${count}${mtime} ${e.msg}`;
	if (!e.fields) return head;
	try {
		return `${head} ${JSON.stringify(e.fields, errorReplacer)}`;
	} catch {
		return `${head} <unserializable fields>`;
	}
}

/** Drain the ring buffer to a human-readable transcript for bug reports. Order
 *  is oldest→newest. The buffer is left intact — calling this never loses logs.
 *  Pass { json: true } for a newline-delimited-JSON dump. */
export function exportLogs(opts?: { json?: boolean }): string {
	const have = Math.min(totalWritten, ring.length);
	if (have === 0) return opts?.json ? "" : "(no logs)";
	const lines: string[] = [];
	for (let i = 0; i < have; i++) {
		// ringHead points at the oldest entry once full, else at ring.length.
		const idx = ring.length < RING_SIZE ? i : (ringHead + i) % RING_SIZE;
		const e = ring[idx];
		lines.push(opts?.json ? JSON.stringify(e, errorReplacer) : formatEntry(e));
	}
	return lines.join("\n");
}

/** Current number of entries retained (capped at RING_SIZE). */
export function retainedLogCount(): number {
	return Math.min(totalWritten, ring.length);
}

// --- console sink --------------------------------------------------------

const CONSOLE_METHOD: Record<LogLevel, "debug" | "info" | "warn" | "error"> = {
	trace: "debug",
	debug: "debug",
	info: "info",
	warn: "warn",
	error: "error",
};

function consoleSink(e: LogEntry): void {
	const fn = console[CONSOLE_METHOD[e.level]];
	const mtime = e.mts !== undefined ? ` t=${formatMediaTime(e.mts)}` : "";
	const prefix = `[${e.tag}]${e.count > 1 ? ` ×${e.count}` : ""}${mtime}`;
	// Pass fields as a separate inspectable argument rather than stringifying —
	// keeps objects expandable in devtools.
	if (e.fields) fn(prefix, e.msg, e.fields);
	else fn(prefix, e.msg);
}
