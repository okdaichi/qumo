// @qumo/log — media-domain layer.
//
// The generic engine (log.ts) speaks the vocabulary of any app: levels, tags,
// messages, fields. This layer speaks the vocabulary of real-time media:
// curated subsystem tags, typed meters (fps / bitrate / rate / gauge), media
// timestamps (frame PTS) stamped onto every log, and a frame() shortcut.
//
// It is stack-agnostic: it does not import or depend on WebCodecs, WebTransport,
// MSE, WebRTC, or @qumo/moq. The caller feeds it numbers (frame counts, byte
// counts, PTS, queue depths) and it produces the rates and summaries a media
// pipeline needs. Nothing here runs on the per-frame hot path beyond bumping a
// number — mark()/sample() are O(1) and allocation-free; the shared 1s pulse in
// log.ts does the formatting and emission.

import { addFlush, createLogger, emit, type Fields, type Logger, type LogLevel } from "./log.ts";

/** Curated media subsystem tags. Use directly or as the base of a dotted child
 *  (e.g. `${MediaTags.video}.decode`). Free-form strings are still accepted
 *  everywhere — these are just the common ones, spelled once. */
export const MediaTags = {
	audio: "audio",
	video: "video",
	encoder: "encoder",
	decoder: "decoder",
	renderer: "renderer",
	capture: "capture",
	transport: "transport",
	network: "network",
	moq: "moq",
	quic: "quic",
	webtransport: "webtransport",
	jitter: "jitter",
	catalog: "catalog",
} as const;
export type MediaTag = (typeof MediaTags)[keyof typeof MediaTags];

// --- meters --------------------------------------------------------------
//
// Four kinds, all flushed once per second by the shared pulse:
//   fps     — counts frames;            flushes "<name>: <n.n> fps (total N)"
//   bitrate — accumulates bytes;        flushes "<name>: <X.XX Mbps>"
//   rate    — counts events;            flushes "<name>: <n.n>/s (total N)"
//   gauge   — samples an instant value; flushes "<name>: avg X (min A, max B)"

type MeterKind = "fps" | "bitrate" | "rate" | "gauge";

/** Handle returned by meter.fps / meter.bitrate / meter.rate. mark() is O(1). */
export interface Meter {
	mark(n?: number): void;
}

/** Handle returned by meter.gauge. sample() is O(1) (keeps a running
 *  min/max/sum/count for the window — no per-sample allocation). */
export interface Gauge {
	sample(value: number): void;
}

export interface MeterOptions {
	/** Suffix appended to gauge summaries (e.g. "ms", " frames"). fps/bitrate
	 *  derive their own units and ignore this. */
	unit?: string;
}

interface MeterState {
	readonly tag: string;
	readonly name: string;
	readonly kind: MeterKind;
	readonly unit?: string;
	// counters (fps / rate / bitrate): delta = frames | events | bytes this window
	delta: number;
	total: number;
	// gauge: running aggregates for the window
	gCount: number;
	gSum: number;
	gMin: number;
	gMax: number;
}

const meters: MeterState[] = [];
let meterHookRegistered = false;

function ensureMeterHook(): void {
	if (meterHookRegistered) return;
	meterHookRegistered = true;
	const secs = 1; // pulse interval is 1s (PULSE_MS in log.ts)
	addFlush(() => {
		for (const m of meters) {
			switch (m.kind) {
				case "fps": {
					if (m.delta === 0) break;
					const per = m.delta / secs;
					emit("info", m.tag, `${m.name}: ${per.toFixed(1)} fps (total ${m.total})`);
					m.delta = 0;
					break;
				}
				case "rate": {
					if (m.delta === 0) break;
					const per = m.delta / secs;
					emit("info", m.tag, `${m.name}: ${per.toFixed(1)}/s (total ${m.total})`);
					m.delta = 0;
					break;
				}
				case "bitrate": {
					if (m.delta === 0) break;
					// delta is bytes this window → bits per second.
					const bps = (m.delta * 8) / secs;
					emit("info", m.tag, `${m.name}: ${formatBitrate(bps)}`);
					m.delta = 0;
					break;
				}
				case "gauge": {
					if (m.gCount === 0) break;
					const avg = m.gSum / m.gCount;
					const unit = m.unit ?? "";
					emit(
						"info",
						m.tag,
						`${m.name}: avg ${round1(avg)}${unit} (min ${round1(m.gMin)}${unit}, max ${
							round1(m.gMax)
						}${unit})`,
					);
					m.gCount = 0;
					m.gSum = 0;
					m.gMin = Number.POSITIVE_INFINITY;
					m.gMax = Number.NEGATIVE_INFINITY;
					break;
				}
			}
		}
	});
}

function registerMeter(
	tag: string,
	name: string,
	kind: MeterKind,
	opts?: MeterOptions,
): MeterState {
	let state = meters.find((m) => m.tag === tag && m.name === name && m.kind === kind);
	if (!state) {
		state = {
			tag,
			name,
			kind,
			unit: opts?.unit,
			delta: 0,
			total: 0,
			gCount: 0,
			gSum: 0,
			gMin: Number.POSITIVE_INFINITY,
			gMax: Number.NEGATIVE_INFINITY,
		};
		meters.push(state);
		ensureMeterHook();
	}
	return state;
}

function meterHandle(state: MeterState): Meter {
	return {
		mark(n = 1) {
			state.delta += n;
			state.total += n;
		},
	};
}

function gaugeHandle(state: MeterState): Gauge {
	return {
		sample(value: number) {
			state.gCount++;
			state.gSum += value;
			if (value < state.gMin) state.gMin = value;
			if (value > state.gMax) state.gMax = value;
		},
	};
}

function round1(n: number): string {
	return (Math.round(n * 10) / 10).toString();
}

/** Human-readable bitrate from bits-per-second. */
export function formatBitrate(bps: number): string {
	if (bps < 1000) return `${Math.round(bps)} bps`;
	if (bps < 1_000_000) return `${(bps / 1_000).toFixed(1)} kbps`;
	if (bps < 1_000_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbps`;
	return `${(bps / 1_000_000_000).toFixed(2)} Gbps`;
}

// --- media logger --------------------------------------------------------

export interface MediaMeterNamespace {
	/** Frame counter → flushes "<name>: <n.n> fps (total N)" once per second. */
	fps(name: string): Meter;
	/** Byte accumulator → flushes "<name>: <X.XX Mbps>" once per second.
	 *  mark(byteLength) per frame. */
	bitrate(name: string): Meter;
	/** Generic event counter → flushes "<name>: <n.n>/s (total N)". */
	rate(name: string): Meter;
	/** Instantaneous sample (decode-queue depth, RTT, jitter ms) → flushes
	 *  "<name>: avg X (min A, max B)" once per second. */
	gauge(name: string, opts?: MeterOptions): Gauge;
}

export interface MediaLogger extends Logger {
	/** Emit a media-frame event. If `fields.pts` is a number it is used as the
	 *  media timestamp (mts) for this entry — more accurate than the clock for
	 *  frame events. Otherwise the bound clock (if any) supplies mts. Use for
	 *  low-frequency frame events (keyframe, config change, corruption); do not
	 *  call per-frame on a hot path — use the meters for per-frame accounting. */
	frame(level: LogLevel, msg: string, fields?: Fields): void;
	/** Typed media aggregators, flushed once per second by the shared pulse. */
	readonly meter: MediaMeterNamespace;
}

export interface MediaLoggerOptions {
	/** Supplies the current media time in microseconds, stamped as `mts` on every
	 *  entry this logger emits (so logs align to the stream timeline). Optional;
	 *  when unset, no media time is recorded. */
	clock?: () => number;
}

/** Create a media-domain logger. Like createLogger(tag), but every emitted
 *  entry carries a media timestamp (when `clock` is provided) and the logger
 *  exposes typed meters (fps/bitrate/rate/gauge) plus a frame() shortcut.
 *
 *  `tag` is typically one of the {@link MediaTags} (e.g. MediaTags.video) or a
 *  dotted child like "video.decode". */
export function createMediaLogger(
	tag: string | MediaTag,
	opts?: MediaLoggerOptions,
): MediaLogger {
	const base = createLogger(tag);
	const clock = opts?.clock;
	const mts = () => clock?.();
	const wrap = (level: LogLevel) => (msg: string, fields?: Fields) =>
		emit(level, tag, msg, fields, mts());

	return {
		trace: wrap("trace"),
		debug: wrap("debug"),
		info: wrap("info"),
		warn: wrap("warn"),
		error: wrap("error"),
		log: (level, msg, fields) => emit(level, tag, msg, fields, mts()),
		throttle: base.throttle,
		counter: base.counter,
		frame: (level, msg, fields) => {
			const pts = fields && typeof fields.pts === "number" ? fields.pts : mts();
			emit(level, tag, msg, fields, pts);
		},
		meter: {
			fps: (name: string) => meterHandle(registerMeter(tag, name, "fps")),
			bitrate: (name: string) => meterHandle(registerMeter(tag, name, "bitrate")),
			rate: (name: string) => meterHandle(registerMeter(tag, name, "rate")),
			gauge: (name: string, o?: MeterOptions) =>
				gaugeHandle(registerMeter(tag, name, "gauge", o)),
		},
	};
}
