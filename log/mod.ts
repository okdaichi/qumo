// @qumo/log — public API surface.
//
// A logging library for real-time media apps. The generic engine (levels, tags,
// ring buffer, dedup, sinks, export) plus a media-domain layer (curated tags,
// fps/bitrate/rate/gauge meters, PTS media-time stamping, frame() shortcut).
//
// Import from the package entry, e.g.:
//   import { createMediaLogger, MediaTags, setLevel } from "@qumo/log";

// Generic engine.
export {
	addFlush,
	addSink,
	createLogger,
	emit,
	exportLogs,
	formatMediaTime,
	getLevel,
	onLevelChange,
	onLogs,
	removeSink,
	retainedLogCount,
	setLevel,
} from "./log.ts";

export type { Counter, Fields, LogEntry, Logger, LogLevel, Sink } from "./log.ts";

// Media layer.
export { createMediaLogger, formatBitrate, MediaTags } from "./media.ts";

export type {
	Gauge,
	MediaLogger,
	MediaLoggerOptions,
	MediaMeterNamespace,
	MediaTag,
	Meter,
	MeterOptions,
} from "./media.ts";
