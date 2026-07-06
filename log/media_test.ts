import { assert, assertEquals, assertMatch } from "jsr:@std/assert@^0.225.0";

import {
	addSink,
	createMediaLogger,
	formatBitrate,
	type LogEntry,
	MediaTags,
	removeSink,
} from "./mod.ts";

function capturingSink(): { sink: (e: LogEntry) => void; entries: LogEntry[] } {
	const entries: LogEntry[] = [];
	const sink = (e: LogEntry) => entries.push(e);
	return { sink, entries };
}

const PULSE = 1100; // meters flush on the 1s pulse; wait a touch longer

Deno.test("createMediaLogger stamps mts from the bound clock on every emit", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	// clock returns microseconds; 1.5s → timecode 00:00:01.500
	const log = createMediaLogger(MediaTags.video, { clock: () => 1_500_000 });
	log.info("hello");
	removeSink(sink);
	assertEquals(entries.length, 1);
	assertEquals(entries[0].mts, 1_500_000);
});

Deno.test("frame() uses fields.pts as the media timestamp when present", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger("video", { clock: () => 999_999_999 });
	log.frame("info", "keyframe", { pts: 2_500_000, type: "key" });
	removeSink(sink);
	assertEquals(entries[0].mts, 2_500_000); // pts wins over clock
});

Deno.test("frame() falls back to the clock when pts is absent", () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger("video", { clock: () => 7_500_000 });
	log.frame("info", "config applied");
	removeSink(sink);
	assertEquals(entries[0].mts, 7_500_000);
});

Deno.test("meter.fps flushes a frames-per-second line once per second", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger(MediaTags.video);
	const frames = log.meter.fps("decode");
	for (let i = 0; i < 30; i++) frames.mark();
	await new Promise((r) => setTimeout(r, PULSE));
	removeSink(sink);
	const flush = entries.find((e) => e.msg.startsWith("decode:"));
	assert(flush, "fps meter flushed");
	assertMatch(flush.msg, /^decode: 30\.0 fps \(total 30\)$/);
});

Deno.test("meter.bitrate flushes a Mbps line from accumulated bytes", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger("video");
	const br = log.meter.bitrate("egress");
	// 300_000 bytes/s * 8 = 2_400_000 bps = 2.40 Mbps
	br.mark(300_000);
	await new Promise((r) => setTimeout(r, PULSE));
	removeSink(sink);
	const flush = entries.find((e) => e.msg.startsWith("egress:"));
	assert(flush, "bitrate meter flushed");
	assertMatch(flush.msg, /^egress: 2\.40 Mbps$/);
});

Deno.test("meter.rate flushes an events-per-second line", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger("moq");
	const drops = log.meter.rate("subscribe resets");
	drops.mark(5);
	await new Promise((r) => setTimeout(r, PULSE));
	removeSink(sink);
	const flush = entries.find((e) => e.msg.startsWith("subscribe resets:"));
	assert(flush, "rate meter flushed");
	assertMatch(flush.msg, /^subscribe resets: 5\.0\/s \(total 5\)$/);
});

Deno.test("meter.gauge flushes avg/min/max with a unit suffix", async () => {
	const { sink, entries } = capturingSink();
	addSink(sink);
	const log = createMediaLogger(MediaTags.jitter);
	const q = log.meter.gauge("jitter", { unit: "ms" });
	q.sample(10);
	q.sample(20);
	q.sample(30); // avg 20, min 10, max 30
	await new Promise((r) => setTimeout(r, PULSE));
	removeSink(sink);
	const flush = entries.find((e) => e.msg.startsWith("jitter:"));
	assert(flush, "gauge meter flushed");
	assertMatch(flush.msg, /^jitter: avg 20ms \(min 10ms, max 30ms\)$/);
});

Deno.test("formatBitrate picks the right unit at each threshold", () => {
	assertEquals(formatBitrate(999), "999 bps");
	assertEquals(formatBitrate(1500), "1.5 kbps");
	assertEquals(formatBitrate(2_400_000), "2.40 Mbps");
	assertEquals(formatBitrate(1_500_000_000), "1.50 Gbps");
});

Deno.test("MediaTags exposes the curated subsystem names", () => {
	assertEquals(MediaTags.video, "video");
	assertEquals(MediaTags.webtransport, "webtransport");
	assertEquals(MediaTags.encoder, "encoder");
});
