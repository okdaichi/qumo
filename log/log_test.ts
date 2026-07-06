import { assert, assertEquals, assertNotEquals } from "jsr:@std/assert@^0.225.0";

import {
	addSink,
	createLogger,
	exportLogs,
	getLevel,
	type LogEntry,
	onLevelChange,
	onLogs,
	removeSink,
	retainedLogCount,
	setLevel,
} from "./log.ts";

// Most tests install a capturing sink to observe emitted entries. Each test is
// responsible for removing its sink so they don't leak into siblings.
function capturingSink(): { sink: (e: LogEntry) => void; entries: LogEntry[] } {
	const entries: LogEntry[] = [];
	const sink = (e: LogEntry) => entries.push(e);
	return { sink, entries };
}

Deno.test("level gate: info is the default; trace/debug suppressed", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("t");
	log.trace("t");
	log.debug("d");
	log.info("i");
	removeSink(sink);
	assertEquals(entries.length, 1);
	assertEquals(entries[0].msg, "i");
});

Deno.test("setLevel('trace') surfaces all levels, including trace/debug", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	setLevel("trace");
	const log = createLogger("t2");
	log.trace("t");
	log.debug("d");
	log.info("i");
	log.warn("w");
	log.error("e");
	setLevel("info"); // restore global default
	removeSink(sink);
	assertEquals(entries.length, 5);
});

Deno.test("per-tag override wins over global and is readable via getLevel", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	setLevel("info"); // global
	setLevel("debug", "video"); // per-tag
	const video = createLogger("video");
	const audio = createLogger("audio");
	video.debug("v"); // emitted (per-tag debug)
	audio.debug("a"); // suppressed (global info)
	assertEquals(getLevel("video"), "debug");
	assertEquals(getLevel("audio"), "info");
	removeSink(sink);
	assertEquals(entries.length, 1);
	assertEquals(entries[0].msg, "v");
});

Deno.test("dedup collapses consecutive identical message-only emits into one ×N entry", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("d");
	log.warn("noisy");
	log.warn("noisy");
	log.warn("noisy");
	log.warn("other");
	removeSink(sink);
	// Sink sees the first occurrence; the repeats only bump the buffer count.
	assertEquals(entries.filter((e) => e.msg === "noisy").length, 1);
	assertEquals(entries.find((e) => e.msg === "other")?.count, 1);
	// The accumulated count is visible in the ring buffer / export.
	assert(/×3/.test(exportLogs()));
});

Deno.test("structured logs always emit (never deduped)", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("s");
	log.info("struct", { n: 1 });
	log.info("struct", { n: 2 });
	removeSink(sink);
	assertEquals(entries.length, 2);
});

Deno.test("throttle emits once per window and carries buffered drops on the next emit", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("th");
	log.throttle("warn", "spam", { i: 1 }, 60);
	log.throttle("warn", "spam", { i: 2 }, 60);
	log.throttle("warn", "spam", { i: 3 }, 60);
	assertEquals(entries.length, 1);
	await new Promise((r) => setTimeout(r, 80));
	log.throttle("warn", "spam", { i: 4 }, 60);
	removeSink(sink);
	assertEquals(entries.length, 2);
	assertEquals((entries[1].fields as { dropped: number }).dropped, 2);
});

Deno.test("counter.mark is O(1) and tracks delta + total (flushed by the 1s timer)", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("c");
	const frames = log.counter("frames");
	frames.mark();
	frames.mark(5);
	// mark() must not have emitted anything itself.
	assertEquals(entries.length, 0);
	// Wait for the aggregator's 1s flush.
	await new Promise((r) => setTimeout(r, 1100));
	removeSink(sink);
	const flush = entries.find((e) => e.msg.startsWith("frames:"));
	assert(flush, "counter flushed a summary line");
	assert(/6\/s \(total 6\)/.test(flush.msg), `flush message: ${flush.msg}`);
});

Deno.test("exportLogs: text format pairs dedup count with the message; json == ndjson", () => {
	const log = createLogger("ex");
	log.warn("repeat");
	log.warn("repeat");
	log.error("boom", { err: new Error("x") });
	const txt = exportLogs();
	assert(/×2[^\n]*repeat/.test(txt), `text export pairs ×2 with repeat: ${txt}`);
	const json = exportLogs({ json: true });
	assertEquals(json.split("\n").length, retainedLogCount());
	// Errors are serialized (name/message/stack), not {}.
	assert(json.includes('"message":"x"'), `error serialized in json export: ${json}`);
});

Deno.test("onLogs observes emitted entries synchronously", () => {
	const seen: LogEntry[] = [];
	const off = onLogs((e) => seen.push(e));
	const log = createLogger("obs");
	log.info("hello");
	off();
	log.info("after-unsubscribe");
	assertEquals(seen.length, 1);
	assertEquals(seen[0].msg, "hello");
});

Deno.test("onLevelChange fires when the level changes", () => {
	let calls = 0;
	const off = onLevelChange(() => calls++);
	setLevel("warn");
	setLevel("error", "x"); // per-tag change also fires
	off();
	setLevel("info"); // not observed after unsubscribe
	assertNotEquals(calls, 0);
});

Deno.test("addSink/removeSink", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createLogger("ar");
	log.info("present");
	removeSink(sink);
	log.info("absent");
	assertEquals(entries.length, 1);
});
