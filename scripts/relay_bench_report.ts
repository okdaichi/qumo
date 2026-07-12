// Relay-chain benchmark report generator (Deno/TypeScript, zero dependencies).
//
// Reads the JSONL emitted by the relay-chain benchmarks (one benchResult per
// line, via BENCH_RESULTS_DIR) and produces:
//   - <dir>/results.csv   — one row per record (the authoritative artifact)
//   - <dir>/summary.csv   — one row per group (metrics at the largest K/depth)
//   - <dir>/plots/*.svg   — hand-rolled SVG line/bar charts (no charting dep)
//
// Usage:
//   deno run --allow-read=<dir> --allow-write=<dir> scripts/relay_bench_report.ts <dir>
//
// SVG is emitted directly (no browser, no native deps) so charts render inline
// on GitHub and scale crisply. Defensive: a plot with <2 data points is skipped
// (logged), never fatal — the CSV is the source of truth.
//
// Formatting target: tabs, double quotes, semicolons (matches playground/deno.json).

interface Rec {
	bench?: string;
	group?: string;
	config?: string;
	k?: number;
	depth?: number;
	rate?: string;
	size_b?: number;
	slice?: number;
	median_ms?: number;
	p95_ms?: number;
	p99_ms?: number;
	min_ms?: number;
	loss_pct?: number;
	fps?: number;
	mbps?: number;
	heap_mb?: number;
	goros?: number;
	cpu_ms?: number;
}

interface Pt {
	x: number;
	y: number;
}
interface Series {
	label: string;
	points: Pt[];
}

const dir = Deno.args[0] ?? "results";

function readRecords(dir: string): Rec[] {
	const path = `${dir}/results.jsonl`;
	let text: string;
	try {
		text = Deno.readTextFileSync(path);
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		console.error(`no results file at ${path} (${msg}); nothing to report`);
		Deno.exit(0);
	}
	const recs: Rec[] = [];
	for (const line of text.split("\n")) {
		const trimmed = line.trim();
		if (!trimmed) continue;
		try {
			recs.push(JSON.parse(trimmed) as Rec);
		} catch (e) {
			const msg = e instanceof Error ? e.message : String(e);
			console.warn(`skipping malformed jsonl line: ${msg}`);
		}
	}
	return recs;
}

const num = (v: number | undefined): string => (v === undefined ? "" : String(v));

// CSV columns: a fixed superset, omitempty per cell. No value contains a comma,
// so no quoting is needed (config is "K=4", rate is "100fps").
const COLUMNS: [string, (r: Rec) => string][] = [
	["bench", (r) => r.bench ?? ""],
	["group", (r) => r.group ?? ""],
	["config", (r) => r.config ?? ""],
	["k", (r) => num(r.k)],
	["depth", (r) => num(r.depth)],
	["rate", (r) => r.rate ?? ""],
	["size_b", (r) => num(r.size_b)],
	["slice", (r) => num(r.slice)],
	["median_ms", (r) => num(r.median_ms)],
	["p95_ms", (r) => num(r.p95_ms)],
	["p99_ms", (r) => num(r.p99_ms)],
	["min_ms", (r) => num(r.min_ms)],
	["loss_pct", (r) => num(r.loss_pct)],
	["fps", (r) => num(r.fps)],
	["mbps", (r) => num(r.mbps)],
	["heap_mb", (r) => num(r.heap_mb)],
	["goros", (r) => num(r.goros)],
	["cpu_ms", (r) => num(r.cpu_ms)],
];

function writeCsv(path: string, header: string[], rows: string[][]) {
	const lines = [header.join(","), ...rows.map((r) => r.join(","))];
	Deno.writeTextFileSync(path, lines.join("\n") + "\n");
}

// ---- SVG charting (hand-rolled) ----

const W = 820;
const H = 480;
const M = { top: 48, right: 70, bottom: 56, left: 70 }; // margins

function niceNum(range: number, round: boolean): number {
	const exp = Math.floor(Math.log10(range || 1));
	const frac = (range || 1) / Math.pow(10, exp);
	let nice: number;
	if (round) {
		if (frac < 1.5) nice = 1;
		else if (frac < 3) nice = 2;
		else if (frac < 7) nice = 5;
		else nice = 10;
	} else {
		if (frac <= 1) nice = 1;
		else if (frac <= 2) nice = 2;
		else if (frac <= 5) nice = 5;
		else nice = 10;
	}
	return nice * Math.pow(10, exp);
}

function ticks(min: number, max: number, count = 5): number[] {
	if (min === max) return [min];
	const span = niceNum(max - min, false);
	const step = niceNum(span / (count - 1), true);
	const start = Math.floor(min / step) * step;
	const out: number[] = [];
	for (let v = start; v <= max + 0.5 * step; v += step) out.push(v);
	return out;
}

function esc(s: string): string {
	return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

const COLORS = ["#1f77b4", "#ff7f0e", "#2ca02c", "#d62728", "#9467bd", "#8c564b"];

// lineChart renders one or more series sharing a single y-axis (and x-axis,
// optionally log-scaled — appropriate for K sweeps where K spans 1..128).
function lineChart(
	series: Series[],
	opts: { title: string; xLabel: string; yLabel: string; xLog?: boolean },
): string {
	const plotW = W - M.left - M.right;
	const plotH = H - M.top - M.bottom;
	const all = series.flatMap((s) => s.points).filter((p) => isFinite(p.x) && isFinite(p.y));
	if (all.length < 2) return "";

	let xMin = Math.min(...all.map((p) => p.x));
	let xMax = Math.max(...all.map((p) => p.x));
	let yMin = Math.min(...all.map((p) => p.y));
	let yMax = Math.max(...all.map((p) => p.y));
	if (opts.xLog) {
		xMin = Math.log10(Math.max(1, xMin));
		xMax = Math.log10(Math.max(xMax, xMin + 1));
	} else {
		xMin = Math.min(xMin, 0);
	}
	yMin = Math.min(yMin, 0);
	if (yMax === yMin) yMax = yMin + 1;

	const X = (x: number) => {
		const v = opts.xLog ? Math.log10(Math.max(1, x)) : x;
		return M.left + ((v - xMin) / (xMax - xMin || 1)) * plotW;
	};
	const Y = (y: number) => M.top + plotH - ((y - yMin) / (yMax - yMin || 1)) * plotH;

	const parts: string[] = [];
	parts.push(`<?xml version="1.0" encoding="UTF-8"?>`);
	parts.push(
		`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="sans-serif" font-size="12">`,
	);
	parts.push(`<rect width="100%" height="100%" fill="white"/>`);
	parts.push(
		`<text x="${W / 2}" y="22" text-anchor="middle" font-size="16" font-weight="bold">${
			esc(opts.title)
		}</text>`,
	);

	// y-axis ticks + gridlines
	for (const t of ticks(yMin, yMax)) {
		const y = Y(t);
		if (y < M.top || y > M.top + plotH) continue;
		parts.push(
			`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
		);
		parts.push(
			`<text x="${M.left - 8}" y="${y + 4}" text-anchor="end">${+t.toFixed(3)}</text>`,
		);
	}
	// x-axis ticks
	const xTickVals = opts.xLog
		? series.flatMap((s) => s.points.map((p) => p.x)).filter((v, i, a) => a.indexOf(v) === i)
			.sort((a, b) => a - b)
		: ticks(xMin, xMax);
	for (const t of xTickVals) {
		const x = X(t);
		parts.push(`<line x1="${x}" y1="${M.top}" x2="${x}" y2="${M.top + plotH}" stroke="#eee"/>`);
		parts.push(`<text x="${x}" y="${M.top + plotH + 18}" text-anchor="middle">${t}</text>`);
	}

	// axes
	parts.push(
		`<line x1="${M.left}" y1="${M.top}" x2="${M.left}" y2="${M.top + plotH}" stroke="#333"/>`,
	);
	parts.push(
		`<line x1="${M.left}" y1="${M.top + plotH}" x2="${M.left + plotW}" y2="${
			M.top + plotH
		}" stroke="#333"/>`,
	);
	parts.push(
		`<text x="${M.left + plotW / 2}" y="${H - 12}" text-anchor="middle">${
			esc(opts.xLabel)
		}</text>`,
	);
	parts.push(
		`<text transform="translate(18,${M.top + plotH / 2}) rotate(-90)" text-anchor="middle">${
			esc(opts.yLabel)
		}</text>`,
	);

	// series
	series.forEach((s, i) => {
		if (s.points.length < 1) return;
		const c = COLORS[i % COLORS.length];
		const d = s.points.map((p, j) =>
			`${i === 0 ? "M" : "L"}${X(p.x).toFixed(1)} ${Y(p.y).toFixed(1)}`
		).join(" ");
		parts.push(`<path d="${d}" fill="none" stroke="${c}" stroke-width="2"/>`);
		s.points.forEach((p) =>
			parts.push(
				`<circle cx="${X(p.x).toFixed(1)}" cy="${Y(p.y).toFixed(1)}" r="3" fill="${c}"/>`,
			)
		);
	});

	// legend
	series.filter((s) => s.points.length > 0).forEach((s, i) => {
		const ly = M.top + 4 + i * 18;
		const c = COLORS[series.indexOf(s) % COLORS.length];
		parts.push(
			`<rect x="${M.left + plotW - 150}" y="${ly}" width="12" height="12" fill="${c}"/>`,
		);
		parts.push(`<text x="${M.left + plotW - 132}" y="${ly + 11}">${esc(s.label)}</text>`);
	});

	parts.push(`</svg>`);
	return parts.join("\n");
}

// barChart renders labelled bars (one record → a couple of deltas). Used for the
// reconnect-storm summary (goroutine & heap delta vs baseline).
function barChart(
	bars: { label: string; value: number }[],
	opts: { title: string; yLabel: string },
): string {
	const plotW = W - M.left - M.right;
	const plotH = H - M.top - M.bottom;
	const vals = bars.map((b) => b.value).filter(isFinite);
	if (vals.length === 0) return "";
	let yMin = Math.min(0, ...vals);
	let yMax = Math.max(1, ...vals);
	const Y = (y: number) => M.top + plotH - ((y - yMin) / (yMax - yMin || 1)) * plotH;
	const bw = plotW / (bars.length * 2);

	const parts: string[] = [];
	parts.push(`<?xml version="1.0" encoding="UTF-8"?>`);
	parts.push(
		`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="sans-serif" font-size="12">`,
	);
	parts.push(`<rect width="100%" height="100%" fill="white"/>`);
	parts.push(
		`<text x="${W / 2}" y="22" text-anchor="middle" font-size="16" font-weight="bold">${
			esc(opts.title)
		}</text>`,
	);
	for (const t of ticks(yMin, yMax)) {
		const y = Y(t);
		parts.push(
			`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
		);
		parts.push(
			`<text x="${M.left - 8}" y="${y + 4}" text-anchor="end">${+t.toFixed(3)}</text>`,
		);
	}
	parts.push(
		`<line x1="${M.left}" y1="${M.top}" x2="${M.left}" y2="${M.top + plotH}" stroke="#333"/>`,
	);
	parts.push(
		`<line x1="${M.left}" y1="${Y(0)}" x2="${M.left + plotW}" y2="${Y(0)}" stroke="#333"/>`,
	);
	bars.forEach((b, i) => {
		const c = COLORS[i % COLORS.length];
		const bx = M.left + i * bw * 2 + bw / 2;
		const by = Y(Math.max(0, b.value));
		const bh = Math.abs(Y(b.value) - Y(0));
		parts.push(`<rect x="${bx}" y="${by}" width="${bw}" height="${bh}" fill="${c}"/>`);
		parts.push(
			`<text x="${bx + bw / 2}" y="${Y(0) + 18}" text-anchor="middle">${esc(b.label)}</text>`,
		);
		parts.push(
			`<text x="${bx + bw / 2}" y="${by - 4}" text-anchor="middle">${+b.value.toFixed(
				2,
			)}</text>`,
		);
	});
	parts.push(
		`<text transform="translate(18,${M.top + plotH / 2}) rotate(-90)" text-anchor="middle">${
			esc(opts.yLabel)
		}</text>`,
	);
	parts.push(`</svg>`);
	return parts.join("\n");
}

function seriesFromGroups(
	recs: Rec[],
	group: string,
	seriesKey: (r: Rec) => string,
	x: (r: Rec) => number,
	y: (r: Rec) => number,
	yLabel: string,
): Series[] {
	const byKey = new Map<string, Pt[]>();
	for (const r of recs.filter((r) => r.group === group)) {
		const xv = x(r);
		const yv = y(r);
		if (xv === undefined || yv === undefined) continue;
		const key = seriesKey(r);
		if (!byKey.has(key)) byKey.set(key, []);
		byKey.get(key)!.push({ x: xv, y: yv });
	}
	return [...byKey.entries()]
		.sort((a, b) => a[0].localeCompare(b[0]))
		.map(([label, points]) => ({
			label,
			points: points.sort((a, b) => a.x - b.x),
		}));
}

function emit(dir: string, name: string, svg: string) {
	if (!svg) {
		console.log(`  skip ${name}.svg (insufficient data)`);
		return;
	}
	Deno.writeTextFileSync(`${dir}/plots/${name}.svg`, svg);
	console.log(`  wrote plots/${name}.svg`);
}

// ---- main ----

const recs = readRecords(dir);
if (recs.length === 0) {
	console.log("no records; nothing to report");
	Deno.exit(0);
}
Deno.mkdirSync(`${dir}/plots`, { recursive: true });

// results.csv
writeCsv(
	`${dir}/results.csv`,
	COLUMNS.map((c) => c[0]),
	recs.map((r) => COLUMNS.map((c) => c[1](r))),
);
console.log(`wrote results.csv (${recs.length} records)`);

// summary.csv — per group, metrics at the largest K (or depth, or last slice).
const groups = [...new Set(recs.map((r) => r.group))];
const sumRows: string[][] = [];
for (const g of groups) {
	const gr = recs.filter((r) => r.group === g);
	const rank = (r: Rec) => r.k ?? r.depth ?? r.slice ?? 0;
	const best = gr.reduce((a, b) => (rank(b) > rank(a) ? b : a));
	sumRows.push([
		g ?? "",
		String(gr.length),
		num(best.k ?? best.depth ?? best.slice),
		best.config ?? "",
		num(best.median_ms),
		num(best.p99_ms),
		num(best.loss_pct),
		num(best.fps),
		num(best.mbps),
		num(best.heap_mb),
		num(best.goros),
	]);
}
writeCsv(
	`${dir}/summary.csv`,
	[
		"group",
		"n_records",
		"max_k_or_depth",
		"config",
		"median_ms",
		"p99_ms",
		"loss_pct",
		"fps",
		"mbps",
		"heap_mb",
		"goros",
	],
	sumRows,
);
console.log(`wrote summary.csv (${groups.length} groups)`);

// plots
console.log("plots:");
// fanout: latency (median/p95/p99), loss, throughput, resources
const fanout = recs.filter((r) => r.group === "fanout");
if (fanout.length >= 2) {
	const byK = new Map<number, Rec>();
	for (const r of fanout) if (r.k !== undefined) byK.set(r.k, r);
	const ks = [...byK.keys()].sort((a, b) => a - b);
	const mk = (field: keyof Rec, label: string): Series => ({
		label,
		points: ks.map((k) => ({ x: k, y: (byK.get(k)![field] ?? 0) as number })),
	});
	emit(
		dir,
		"fanout_latency",
		lineChart(
			[mk("median_ms", "median"), mk("p95_ms", "p95"), mk("p99_ms", "p99")],
			{
				title: "Fan-out latency vs K",
				xLabel: "K (leaves)",
				yLabel: "latency (ms)",
				xLog: true,
			},
		),
	);
	emit(
		dir,
		"fanout_loss",
		lineChart([mk("loss_pct", "loss%")], {
			title: "Frame loss vs K",
			xLabel: "K (leaves)",
			yLabel: "loss %",
			xLog: true,
		}),
	);
	emit(
		dir,
		"fanout_throughput",
		lineChart(
			[mk("fps", "fps"), mk("mbps", "Mbps")],
			{ title: "Throughput vs K", xLabel: "K (leaves)", yLabel: "fps / Mbps", xLog: true },
		),
	);
	emit(
		dir,
		"fanout_heap",
		lineChart([mk("heap_mb", "heap MB")], {
			title: "Heap vs K",
			xLabel: "K (leaves)",
			yLabel: "heap (MB)",
			xLog: true,
		}),
	);
	emit(
		dir,
		"fanout_goros",
		lineChart([mk("goros", "goroutines")], {
			title: "Goroutines vs K",
			xLabel: "K (leaves)",
			yLabel: "goroutines",
			xLog: true,
		}),
	);
}

// load: p99 vs K, one line per rate
emit(
	dir,
	"load_latency",
	lineChart(
		seriesFromGroups(
			recs,
			"load",
			(r) => r.rate ?? "?",
			(r) => r.k ?? 0,
			(r) => r.p99_ms ?? 0,
			"p99",
		),
		{ title: "Load: p99 latency vs K", xLabel: "K (leaves)", yLabel: "p99 (ms)", xLog: true },
	),
);
// objsize: p99 and throughput vs K, one line per object size
emit(
	dir,
	"objsize_latency",
	lineChart(
		seriesFromGroups(
			recs,
			"objsize",
			(r) => `${r.size_b}B`,
			(r) => r.k ?? 0,
			(r) => r.p99_ms ?? 0,
			"p99",
		),
		{
			title: "Object size: p99 latency vs K",
			xLabel: "K (leaves)",
			yLabel: "p99 (ms)",
			xLog: true,
		},
	),
);
emit(
	dir,
	"objsize_throughput",
	lineChart(
		seriesFromGroups(
			recs,
			"objsize",
			(r) => `${r.size_b}B`,
			(r) => r.k ?? 0,
			(r) => r.mbps ?? 0,
			"Mbps",
		),
		{ title: "Object size: throughput vs K", xLabel: "K (leaves)", yLabel: "Mbps", xLog: true },
	),
);
// series: per-hop latency (median vs depth)
emit(
	dir,
	"series_perhop",
	lineChart(
		seriesFromGroups(
			recs,
			"series",
			() => "median",
			(r) => r.depth ?? 0,
			(r) => r.median_ms ?? 0,
			"ms",
		),
		{ title: "Per-hop latency vs chain depth", xLabel: "depth (hops)", yLabel: "median (ms)" },
	),
);
// soak: p99 per time-slice (drift indicator)
emit(
	dir,
	"soak_latency",
	lineChart(
		seriesFromGroups(
			recs,
			"soak",
			() => "p99",
			(r) => r.slice ?? 0,
			(r) => r.p99_ms ?? 0,
			"ms",
		),
		{ title: "Soak: p99 latency per time-slice", xLabel: "slice", yLabel: "p99 (ms)" },
	),
);
// reconnect: goroutine + heap delta bars
const rc = recs.find((r) => r.group === "reconnect");
if (rc) {
	emit(
		dir,
		"reconnect",
		barChart(
			[
				{ label: "goros Δ", value: rc.goros ?? 0 },
				{ label: "heap Δ MB", value: rc.heap_mb ?? 0 },
			],
			{ title: `Reconnect storm (${rc.config ?? ""})`, yLabel: "delta vs baseline" },
		),
	);
}

console.log("done.");
