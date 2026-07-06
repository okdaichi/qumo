// @okdaichi/log — public API surface.
//
// Import from the package entry, e.g.:
//   import { createLogger, setLevel, exportLogs } from "@okdaichi/log";

export {
	addSink,
	createLogger,
	exportLogs,
	getLevel,
	onLevelChange,
	onLogs,
	removeSink,
	retainedLogCount,
	setLevel,
} from "./log.ts";

export type { Counter, Fields, LogEntry, Logger, LogLevel, Sink } from "./log.ts";
