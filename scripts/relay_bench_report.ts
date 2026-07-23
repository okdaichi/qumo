// Relay-chain benchmark report generator (Deno/TypeScript, zero dependencies).
//
// Reads the JSONL emitted by the relay-chain benchmarks (one benchResult per
// line, via BENCH_RESULTS_DIR) and produces:
//   - <dir>/results.csv   — one row per record (the authoritative artifact)
//   - <dir>/summary.csv   — one row per group + decision-grade derived values
//                           (per-hop latency slope, fan-out knee K)
//   - <dir>/derived.csv   — the headline decision numbers in one file
//   - <dir>/plots/*.svg   — hand-rolled SVG charts (no charting dep):
//                           line + regression fit, box-and-whisker, bar,
//                           and a 3-panel overview
//   - <dir>/index.html    — ONE consolidated, self-contained dashboard: the
//                           capacity headline (concurrent-session ceiling),
//                           decision summary, every plot inline, and — when a
//                           paramexp report dir is supplied — the GP/ML findings
//                           (knees, η² importance, interactions, suggested-next).
//                           This is the "easy to see" surface: open it, no server.
//
// Usage:
//   deno run --allow-read=<dir> --allow-write=<dir> scripts/relay_bench_report.ts <dir>
//   deno run --allow-read --allow-write scripts/relay_bench_report.ts <dir> --paramexp <px-report-dir>
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
	min_ms?: number;
	p25_ms?: number;
	median_ms?: number;
	p75_ms?: number;
	p95_ms?: number;
	p99_ms?: number;
	max_ms?: number;
	loss_pct?: number;
	fps?: number;
	mbps?: number;
	heap_mb?: number;
	goros?: number;
	cpu_ms?: number;
	fairness?: number; // Jain's index 0-1
	// capacity group (BenchmarkRelay_ConnectionCarry): concurrent-session axis.
	sessions?: number;
	connected?: number;
	receiving?: number;
	per_session_kb?: number;
	verdict?: string; // HOLDS | CANNOT-HOLD
}

interface Pt {
	x: number;
	y: number;
}
interface Series {
	label: string;
	points: Pt[];
}
interface Box {
	x: number;
	min: number;
	q1: number;
	median: number;
	q3: number;
	max: number;
}
interface Fit {
	slope: number;
	intercept: number;
	r2: number;
}

const dir = Deno.args[0] ?? "results";
// Optional paramexp report dir (report.json + SVGs from the GP/ML analysis).
// When present, the consolidated dashboard folds in knees / importance /
// interactions / suggested-next. Absent → the ML section is simply omitted.
function argValue(flag: string): string | undefined {
	const i = Deno.args.indexOf(flag);
	return i >= 0 && i + 1 < Deno.args.length ? Deno.args[i + 1] : undefined;
}
const paramexpDir = argValue("--paramexp");

// Every SVG the report emits is also collected here so the dashboard can inline
// it (one self-contained index.html, no external image requests).
const collected: { name: string; svg: string }[] = [];

function readRecords(d: string): Rec[] {
	const path = `${d}/results.jsonl`;
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

// CSV columns: a fixed superset, omitempty per cell. No value contains a comma.
const COLUMNS: [string, (r: Rec) => string][] = [
	["bench", (r) => r.bench ?? ""],
	["group", (r) => r.group ?? ""],
	["config", (r) => r.config ?? ""],
	["k", (r) => num(r.k)],
	["depth", (r) => num(r.depth)],
	["rate", (r) => r.rate ?? ""],
	["size_b", (r) => num(r.size_b)],
	["slice", (r) => num(r.slice)],
	["min_ms", (r) => num(r.min_ms)],
	["p25_ms", (r) => num(r.p25_ms)],
	["median_ms", (r) => num(r.median_ms)],
	["p75_ms", (r) => num(r.p75_ms)],
	["p95_ms", (r) => num(r.p95_ms)],
	["p99_ms", (r) => num(r.p99_ms)],
	["max_ms", (r) => num(r.max_ms)],
	["loss_pct", (r) => num(r.loss_pct)],
	["fps", (r) => num(r.fps)],
	["mbps", (r) => num(r.mbps)],
	["heap_mb", (r) => num(r.heap_mb)],
	["goros", (r) => num(r.goros)],
	["cpu_ms", (r) => num(r.cpu_ms)],
	["fairness", (r) => num(r.fairness)],
	["sessions", (r) => num(r.sessions)],
	["connected", (r) => num(r.connected)],
	["receiving", (r) => num(r.receiving)],
	["per_session_kb", (r) => num(r.per_session_kb)],
	["verdict", (r) => r.verdict ?? ""],
];

function writeCsv(path: string, header: string[], rows: string[][]) {
	const lines = [header.join(","), ...rows.map((r) => r.join(","))];
	Deno.writeTextFileSync(path, lines.join("\n") + "\n");
}

// ---- stats helpers ----

// linearRegression least-squares fits y = slope*x + intercept over the points
// and returns r². Null if underdetermined.
function linearRegression(pts: Pt[]): Fit | null {
	if (pts.length < 2) return null;
	const n = pts.length;
	const sx = pts.reduce((a, p) => a + p.x, 0);
	const sy = pts.reduce((a, p) => a + p.y, 0);
	const sxx = pts.reduce((a, p) => a + p.x * p.x, 0);
	const sxy = pts.reduce((a, p) => a + p.x * p.y, 0);
	const denom = n * sxx - sx * sx;
	if (denom === 0) return null;
	const slope = (n * sxy - sx * sy) / denom;
	const intercept = (sy - slope * sx) / n;
	const my = sy / n;
	let ssTot = 0;
	let ssRes = 0;
	for (const p of pts) {
		const yhat = slope * p.x + intercept;
		ssTot += (p.y - my) ** 2;
		ssRes += (p.y - yhat) ** 2;
	}
	const r2 = ssTot === 0 ? 1 : 1 - ssRes / ssTot;
	return { slope, intercept, r2 };
}

// ---- SVG scaffolding (hand-rolled) ----

const W = 820;
const H = 480;
const M = { top: 48, right: 70, bottom: 56, left: 70 };

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

function svgWrap(title: string, inner: string[]): string {
	const parts = [
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="sans-serif" font-size="12">`,
		`<rect width="100%" height="100%" fill="white"/>`,
		`<text x="${W / 2}" y="22" text-anchor="middle" font-size="16" font-weight="bold">${
			esc(title)
		}</text>`,
	];
	parts.push(...inner);
	parts.push(`</svg>`);
	return parts.join("\n");
}

// lineChart renders ≥1 series sharing one y-axis. If opts.fit is set, a
// least-squares line is overlaid on the FIRST series with a slope/R² annotation.
function lineChart(
	series: Series[],
	opts: { title: string; xLabel: string; yLabel: string; xLog?: boolean; fit?: boolean },
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

	const inner: string[] = [];
	for (const t of ticks(yMin, yMax)) {
		const y = Y(t);
		if (y < M.top || y > M.top + plotH) continue;
		inner.push(
			`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
		);
		inner.push(
			`<text x="${M.left - 8}" y="${y + 4}" text-anchor="end">${+t.toFixed(3)}</text>`,
		);
	}
	const xTickVals = opts.xLog
		? [...new Set(all.map((p) => p.x))].sort((a, b) => a - b)
		: ticks(xMin, xMax);
	for (const t of xTickVals) {
		const x = X(t);
		inner.push(`<line x1="${x}" y1="${M.top}" x2="${x}" y2="${M.top + plotH}" stroke="#eee"/>`);
		inner.push(`<text x="${x}" y="${M.top + plotH + 18}" text-anchor="middle">${t}</text>`);
	}
	inner.push(
		`<line x1="${M.left}" y1="${M.top}" x2="${M.left}" y2="${M.top + plotH}" stroke="#333"/>`,
	);
	inner.push(
		`<line x1="${M.left}" y1="${M.top + plotH}" x2="${M.left + plotW}" y2="${
			M.top + plotH
		}" stroke="#333"/>`,
	);
	inner.push(
		`<text x="${M.left + plotW / 2}" y="${H - 12}" text-anchor="middle">${
			esc(opts.xLabel)
		}</text>`,
	);
	inner.push(
		`<text transform="translate(18,${M.top + plotH / 2}) rotate(-90)" text-anchor="middle">${
			esc(opts.yLabel)
		}</text>`,
	);

	series.forEach((s, i) => {
		if (s.points.length < 1) return;
		const c = COLORS[i % COLORS.length];
		const d = s.points.map((p) => `M${X(p.x).toFixed(1)} ${Y(p.y).toFixed(1)}`).join(" ");
		inner.push(`<path d="${d}" fill="none" stroke="${c}" stroke-width="2"/>`);
		s.points.forEach((p) =>
			inner.push(
				`<circle cx="${X(p.x).toFixed(1)}" cy="${Y(p.y).toFixed(1)}" r="3" fill="${c}"/>`,
			)
		);
	});

	// regression fit overlay on the first series
	if (opts.fit && series[0]?.points.length) {
		const fitPts = series[0].points;
		const fit = linearRegression(
			opts.xLog ? fitPts.map((p) => ({ x: Math.log10(Math.max(1, p.x)), y: p.y })) : fitPts,
		);
		if (fit) {
			const fx0 = opts.xLog ? Math.log10(Math.max(1, fitPts[0].x)) : fitPts[0].x;
			const fx1 = opts.xLog
				? Math.log10(Math.max(1, fitPts[fitPts.length - 1].x))
				: fitPts[fitPts.length - 1].x;
			const y0 = fit.slope * fx0 + fit.intercept;
			const y1 = fit.slope * fx1 + fit.intercept;
			inner.push(
				`<line x1="${X(fitPts[0].x).toFixed(1)}" y1="${Y(y0).toFixed(1)}" x2="${
					X(fitPts[fitPts.length - 1].x).toFixed(1)
				}" y2="${
					Y(y1).toFixed(1)
				}" stroke="#000" stroke-width="1.5" stroke-dasharray="5,3"/>`,
			);
			const unit = opts.xLabel.includes("depth") ? "/hop" : "/K";
			inner.push(
				`<text x="${M.left + plotW - 16}" y="${
					H - 30
				}" text-anchor="end" font-style="italic">fit: ${
					fit.slope.toFixed(3)
				} ms${unit}, R²=${fit.r2.toFixed(3)}</text>`,
			);
		}
	}

	series.filter((s) => s.points.length > 0).forEach((s) => {
		const ly = M.top + 4 + series.indexOf(s) * 18;
		const c = COLORS[series.indexOf(s) % COLORS.length];
		inner.push(
			`<rect x="${M.left + plotW - 150}" y="${ly}" width="12" height="12" fill="${c}"/>`,
		);
		inner.push(`<text x="${M.left + plotW - 132}" y="${ly + 11}">${esc(s.label)}</text>`);
	});

	return svgWrap(opts.title, inner);
}

// boxPlot renders a box-and-whisker per Box (min—Q1—median—Q3—max) vs x.
function boxPlot(boxes: Box[], opts: { title: string; xLabel: string; yLabel: string }): string {
	const plotW = W - M.left - M.right;
	const plotH = H - M.top - M.bottom;
	const valid = boxes.filter((b) => isFinite(b.median));
	if (valid.length < 1) return "";
	let yMin = Math.min(...valid.map((b) => b.min));
	let yMax = Math.max(...valid.map((b) => b.max));
	yMin = Math.min(yMin, 0);
	if (yMax === yMin) yMax = yMin + 1;
	const X = (i: number) =>
		M.left + (valid.length === 1 ? plotW / 2 : (i / (valid.length - 1)) * plotW);
	const Y = (y: number) => M.top + plotH - ((y - yMin) / (yMax - yMin || 1)) * plotH;
	const bw = Math.min(48, plotW / (valid.length * 2.2));

	const inner: string[] = [];
	for (const t of ticks(yMin, yMax)) {
		const y = Y(t);
		inner.push(
			`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
		);
		inner.push(
			`<text x="${M.left - 8}" y="${y + 4}" text-anchor="end">${+t.toFixed(3)}</text>`,
		);
	}
	inner.push(
		`<line x1="${M.left}" y1="${M.top}" x2="${M.left}" y2="${M.top + plotH}" stroke="#333"/>`,
	);
	inner.push(
		`<line x1="${M.left}" y1="${M.top + plotH}" x2="${M.left + plotW}" y2="${
			M.top + plotH
		}" stroke="#333"/>`,
	);
	inner.push(
		`<text x="${M.left + plotW / 2}" y="${H - 12}" text-anchor="middle">${
			esc(opts.xLabel)
		}</text>`,
	);
	inner.push(
		`<text transform="translate(18,${M.top + plotH / 2}) rotate(-90)" text-anchor="middle">${
			esc(opts.yLabel)
		}</text>`,
	);

	valid.forEach((b, i) => {
		const cx = X(i);
		const c = COLORS[i % COLORS.length];
		// whiskers (min–Q1, Q3–max)
		inner.push(
			`<line x1="${cx}" y1="${Y(b.min)}" x2="${cx}" y2="${
				Y(b.q1)
			}" stroke="#555" stroke-width="1.5"/>`,
		);
		inner.push(
			`<line x1="${cx}" y1="${Y(b.q3)}" x2="${cx}" y2="${
				Y(b.max)
			}" stroke="#555" stroke-width="1.5"/>`,
		);
		inner.push(
			`<line x1="${cx - bw / 3}" y1="${Y(b.min)}" x2="${cx + bw / 3}" y2="${
				Y(b.min)
			}" stroke="#555"/>`,
		);
		inner.push(
			`<line x1="${cx - bw / 3}" y1="${Y(b.max)}" x2="${cx + bw / 3}" y2="${
				Y(b.max)
			}" stroke="#555"/>`,
		);
		// box Q1–Q3
		const by = Y(b.q3);
		const bh = Y(b.q1) - Y(b.q3);
		inner.push(
			`<rect x="${
				cx - bw / 2
			}" y="${by}" width="${bw}" height="${bh}" fill="${c}" fill-opacity="0.3" stroke="${c}" stroke-width="1.5"/>`,
		);
		// median line
		inner.push(
			`<line x1="${cx - bw / 2}" y1="${Y(b.median)}" x2="${cx + bw / 2}" y2="${
				Y(b.median)
			}" stroke="${c}" stroke-width="2.5"/>`,
		);
		// x label
		inner.push(`<text x="${cx}" y="${M.top + plotH + 18}" text-anchor="middle">${b.x}</text>`);
	});

	return svgWrap(opts.title, inner);
}

// overview: 3 stacked panels (latency / loss / throughput) sharing the K x-axis,
// for an at-a-glance picture of the fan-out knee.
function overview(
	ks: number[],
	latency: Pt[],
	loss: Pt[],
	throughput: Pt[],
	heap: Pt[],
): string {
	const OH = 760;
	const panels = [
		{ title: "p99 latency (ms)", pts: latency, color: "#d62728" },
		{ title: "frame loss (%)", pts: loss, color: "#ff7f0e" },
		{ title: "throughput (fps)", pts: throughput, color: "#1f77b4" },
		{ title: "heap (MB)", pts: heap, color: "#2ca02c" },
	];
	const top = 44;
	const bottom = 50;
	const gap = 16;
	const plotW = W - M.left - M.right;
	const panelH = (OH - top - bottom - gap * (panels.length - 1)) / panels.length;

	const xMin = Math.log10(Math.max(1, Math.min(...ks)));
	const xMax = Math.log10(Math.max(...ks));
	const X = (x: number, y0: number) =>
		M.left + ((Math.log10(Math.max(1, x)) - xMin) / (xMax - xMin || 1)) * plotW;

	const inner: string[] = [];
	panels.forEach((p, pi) => {
		const yTop = top + pi * (panelH + gap);
		const ys = p.pts.map((q) => q.y).filter(isFinite);
		if (ys.length === 0) return;
		let yMin = Math.min(0, ...ys);
		let yMax = Math.max(1, ...ys);
		if (yMax === yMin) yMax = yMin + 1;
		const Y = (y: number) => yTop + panelH - ((y - yMin) / (yMax - yMin || 1)) * panelH;
		inner.push(`<text x="${M.left}" y="${yTop - 6}" font-weight="bold">${p.title}</text>`);
		for (const t of ticks(yMin, yMax)) {
			const y = Y(t);
			inner.push(
				`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
			);
			inner.push(
				`<text x="${M.left - 6}" y="${y + 3}" text-anchor="end" font-size="10">${+t.toFixed(
					2,
				)}</text>`,
			);
		}
		inner.push(
			`<line x1="${M.left}" y1="${yTop}" x2="${M.left}" y2="${
				yTop + panelH
			}" stroke="#333"/>`,
		);
		inner.push(
			`<line x1="${M.left}" y1="${yTop + panelH}" x2="${M.left + plotW}" y2="${
				yTop + panelH
			}" stroke="#333"/>`,
		);
		const d = p.pts.map((q) => `M${X(q.x, yTop).toFixed(1)} ${Y(q.y).toFixed(1)}`).join(" ");
		inner.push(`<path d="${d}" fill="none" stroke="${p.color}" stroke-width="2"/>`);
		p.pts.forEach((q) =>
			inner.push(
				`<circle cx="${X(q.x, yTop).toFixed(1)}" cy="${
					Y(q.y).toFixed(1)
				}" r="2.5" fill="${p.color}"/>`,
			)
		);
	});
	// shared x labels (bottom panel)
	ks.forEach((k) => {
		inner.push(
			`<text x="${X(k, 0).toFixed(1)}" y="${OH - 18}" text-anchor="middle">K=${k}</text>`,
		);
	});
	inner.push(
		`<text x="${W / 2}" y="${OH - 4}" text-anchor="middle">fan-out width K (log scale)</text>`,
	);

	const parts = [
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${OH}" viewBox="0 0 ${W} ${OH}" font-family="sans-serif" font-size="12">`,
		`<rect width="100%" height="100%" fill="white"/>`,
		`<text x="${
			W / 2
		}" y="22" text-anchor="middle" font-size="16" font-weight="bold">Fan-out overview (latency · loss · throughput · heap vs K)</text>`,
		...inner,
		`</svg>`,
	];
	return parts.join("\n");
}

// barChart renders labelled bars (reconnect-storm deltas vs baseline).
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
	const inner: string[] = [];
	for (const t of ticks(yMin, yMax)) {
		const y = Y(t);
		inner.push(
			`<line x1="${M.left}" y1="${y}" x2="${M.left + plotW}" y2="${y}" stroke="#eee"/>`,
		);
		inner.push(
			`<text x="${M.left - 8}" y="${y + 4}" text-anchor="end">${+t.toFixed(3)}</text>`,
		);
	}
	inner.push(
		`<line x1="${M.left}" y1="${M.top}" x2="${M.left}" y2="${M.top + plotH}" stroke="#333"/>`,
	);
	inner.push(
		`<line x1="${M.left}" y1="${Y(0)}" x2="${M.left + plotW}" y2="${Y(0)}" stroke="#333"/>`,
	);
	bars.forEach((b, i) => {
		const c = COLORS[i % COLORS.length];
		const bx = M.left + i * bw * 2 + bw / 2;
		const by = Y(Math.max(0, b.value));
		const bh = Math.abs(Y(b.value) - Y(0));
		inner.push(`<rect x="${bx}" y="${by}" width="${bw}" height="${bh}" fill="${c}"/>`);
		inner.push(
			`<text x="${bx + bw / 2}" y="${Y(0) + 18}" text-anchor="middle">${esc(b.label)}</text>`,
		);
		inner.push(
			`<text x="${bx + bw / 2}" y="${by - 4}" text-anchor="middle">${+b.value.toFixed(
				2,
			)}</text>`,
		);
	});
	inner.push(
		`<text transform="translate(18,${M.top + plotH / 2}) rotate(-90)" text-anchor="middle">${
			esc(opts.yLabel)
		}</text>`,
	);
	return svgWrap(opts.title, inner);
}

function seriesFromGroups(
	recs: Rec[],
	group: string,
	seriesKey: (r: Rec) => string,
	x: (r: Rec) => number,
	y: (r: Rec) => number,
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
		.map(([label, points]) => ({ label, points: points.sort((a, b) => a.x - b.x) }));
}

function emit(d: string, name: string, svg: string) {
	if (!svg) {
		console.log(`  skip ${name}.svg (insufficient data)`);
		return;
	}
	Deno.writeTextFileSync(`${d}/plots/${name}.svg`, svg);
	collected.push({ name, svg });
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

// ---- decision-grade derived values ----
// Per-hop latency slope: linear fit of series median vs depth (ms per hop).
const seriesRecs = recs.filter((r) =>
	r.group === "series" && r.depth !== undefined && r.median_ms !== undefined
);
const perHop = linearRegression(seriesRecs.map((r) => ({ x: r.depth!, y: r.median_ms! })));
// Fan-out knee: smallest K whose loss% crosses the threshold (else "none").
const KNEE_LOSS_PCT = 1.0;
const fanoutRecs = recs
	.filter((r) => r.group === "fanout" && r.k !== undefined && r.loss_pct !== undefined)
	.sort((a, b) => (a.k! - b.k!));
const kneeRec = fanoutRecs.find((r) => r.loss_pct! >= KNEE_LOSS_PCT);

console.log("\ndecision summary:");
if (perHop) {
	console.log(
		`  per-hop latency slope: ${perHop.slope.toFixed(3)} ms/hop (R²=${perHop.r2.toFixed(3)})`,
	);
}
console.log(
	`  fan-out knee (loss≥${KNEE_LOSS_PCT}%): ${
		kneeRec ? `K=${kneeRec.k}` : "none (loss-free across measured K)"
	}`,
);

// timer-floor warning: on fast loopback, low percentiles measure ≈0 and absolute
// latencies aren't representative. Warn so the plot isn't misread.
const floored = recs.filter((r) => (r.median_ms ?? 1) < 0.001 && r.group !== "reconnect");
if (floored.length > 0) {
	console.log(
		`  ⚠ ${floored.length} record(s) have median latency ≈ 0 — likely timer-floored on fast loopback. Absolute latencies NOT representative; read p99/tail + shape across K.`,
	);
}

// jitter (p99−median) and per-subscriber resource at max K
const maxK = fanoutRecs[fanoutRecs.length - 1];
if (maxK?.p99_ms !== undefined && maxK?.median_ms !== undefined) {
	console.log(
		`  jitter at max K (${maxK.k}): p99−median = ${
			(maxK.p99_ms - maxK.median_ms).toFixed(2)
		} ms`,
	);
}
if (maxK?.k && maxK.k > 0 && maxK.heap_mb !== undefined) {
	console.log(
		`  per-subscriber at K=${maxK.k}: ${(maxK.heap_mb / maxK.k).toFixed(2)} MB heap, ${
			(maxK.goros ?? 0) / maxK.k
		} goros`,
	);
}
if (maxK?.fairness !== undefined) {
	console.log(`  fairness at K=${maxK.k}: ${maxK.fairness.toFixed(3)} (Jain, 1=perfect)`);
}

// summary.csv — per group, metrics at the largest K/depth, plus derived values.
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
writeCsv(`${dir}/summary.csv`, [
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
], sumRows);
// derived.csv — the headline decision numbers in their own one-shot file.
writeCsv(`${dir}/derived.csv`, [
	"metric",
	"value",
	"unit",
	"note",
], [
	[
		"per_hop_latency_slope",
		perHop ? perHop.slope.toFixed(4) : "",
		"ms/hop",
		perHop ? `linear fit R²=${perHop.r2.toFixed(3)}` : "",
	],
	[
		"fanout_knee_k",
		kneeRec ? String(kneeRec.k) : "",
		"subscribers",
		`first K with loss≥${KNEE_LOSS_PCT}%`,
	],
	["fanout_knee_loss_pct", kneeRec ? kneeRec.loss_pct!.toFixed(2) : "", "%", ""],
	[
		"jitter_at_max_k_ms",
		(maxK?.p99_ms !== undefined && maxK?.median_ms !== undefined)
			? (maxK.p99_ms - maxK.median_ms).toFixed(3)
			: "",
		"ms",
		`p99−median at K=${maxK?.k ?? "?"}`,
	],
	[
		"heap_per_subscriber_mb",
		maxK?.k ? (maxK.heap_mb! / maxK.k).toFixed(3) : "",
		"MB/subscriber",
		`at K=${maxK?.k}`,
	],
	[
		"fairness_at_max_k",
		maxK?.fairness !== undefined ? maxK.fairness.toFixed(3) : "",
		"Jain 0-1",
		`at K=${maxK?.k}`,
	],
]);
console.log(`wrote summary.csv (${groups.length} groups) + derived.csv`);

// ---- plots ----
console.log("plots:");
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
		lineChart([mk("p99_ms", "p99"), mk("median_ms", "median")], {
			title: "Fan-out latency vs K (with linear fit)",
			xLabel: "K (leaves)",
			yLabel: "latency (ms)",
			xLog: true,
			fit: true,
		}),
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
		lineChart([mk("fps", "fps"), mk("mbps", "Mbps")], {
			title: "Throughput vs K",
			xLabel: "K (leaves)",
			yLabel: "fps / Mbps",
			xLog: true,
		}),
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
	// box-and-whisker of the latency distribution per K
	const boxes: Box[] = ks.map((k) => {
		const r = byK.get(k)!;
		return {
			x: k,
			min: r.min_ms ?? 0,
			q1: r.p25_ms ?? 0,
			median: r.median_ms ?? 0,
			q3: r.p75_ms ?? 0,
			max: r.max_ms ?? 0,
		};
	}).filter((b) => b.median > 0);
	emit(
		dir,
		"fanout_latency_box",
		boxPlot(boxes, {
			title: "Fan-out latency distribution vs K",
			xLabel: "K (leaves)",
			yLabel: "latency (ms)",
		}),
	);
	// per-subscriber fairness (Jain's index on delivered frames)
	emit(
		dir,
		"fanout_fairness",
		lineChart([mk("fairness", "Jain's fairness")], {
			title: "Per-subscriber fairness vs K",
			xLabel: "K (leaves)",
			yLabel: "Jain's index (1=fair)",
			xLog: true,
		}),
	);
	// overview dashboard (latency / loss / throughput / heap, shared K axis)
	emit(
		dir,
		"overview",
		overview(
			ks,
			ks.map((k) => ({ x: k, y: byK.get(k)!.p99_ms ?? 0 })),
			ks.map((k) => ({ x: k, y: byK.get(k)!.loss_pct ?? 0 })),
			ks.map((k) => ({ x: k, y: byK.get(k)!.fps ?? 0 })),
			ks.map((k) => ({ x: k, y: byK.get(k)!.heap_mb ?? 0 })),
		),
	);
}

// load: p99 vs K, one line per rate
emit(
	dir,
	"load_latency",
	lineChart(
		seriesFromGroups(recs, "load", (r) => r.rate ?? "?", (r) => r.k ?? 0, (r) => r.p99_ms ?? 0),
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
		),
		{ title: "Object size: throughput vs K", xLabel: "K (leaves)", yLabel: "Mbps", xLog: true },
	),
);
// stress: depth-1 capacity vs offered publish rate (loss / throughput / latency)
const stressRecs2 = recs.filter((r) => r.group === "stress" && r.rate);
if (stressRecs2.length >= 2) {
	const fpsOf = (r: Rec) => parseInt((r.rate ?? "0").replace(/\D/g, "")) || 0;
	const srs = stressRecs2.slice().sort((a, b) => fpsOf(a) - fpsOf(b));
	const pts = (field: keyof Rec) =>
		srs.map((r) => ({ x: fpsOf(r), y: (r[field] ?? 0) as number }));
	emit(
		dir,
		"stress_loss",
		lineChart([{ label: "loss%", points: pts("loss_pct") }], {
			title: "Stress: frame loss vs offered load",
			xLabel: "offered load (fps)",
			yLabel: "loss %",
		}),
	);
	emit(
		dir,
		"stress_throughput",
		lineChart(
			[{ label: "delivered fps", points: pts("fps") }, {
				label: "Mbps",
				points: pts("mbps"),
			}],
			{
				title: "Stress: delivered throughput vs offered load",
				xLabel: "offered load (fps)",
				yLabel: "fps / Mbps",
			},
		),
	);
	emit(
		dir,
		"stress_latency",
		lineChart(
			[{ label: "p99", points: pts("p99_ms") }, {
				label: "median",
				points: pts("median_ms"),
			}],
			{
				title: "Stress: latency vs offered load",
				xLabel: "offered load (fps)",
				yLabel: "latency (ms)",
			},
		),
	);
}

// series: per-hop latency (median vs depth) with regression fit
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
		),
		{
			title: "Per-hop latency vs chain depth (with linear fit)",
			xLabel: "depth (hops)",
			yLabel: "median (ms)",
			fit: true,
		},
	),
);
// soak: p99 per time-slice (drift indicator)
emit(
	dir,
	"soak_latency",
	lineChart(
		seriesFromGroups(recs, "soak", () => "p99", (r) => r.slice ?? 0, (r) => r.p99_ms ?? 0),
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

// ---- consolidated dashboard (index.html) ----
// One self-contained page: capacity headline + decision summary + every plot
// inline + (optional) the paramexp GP/ML findings. This is the "easy to see"
// surface — open results/index.html, no server, no external requests.

interface PxVector {
	[k: string]: string;
}
interface PxKnee {
	param?: string;
	value?: string;
	score?: number;
}
interface PxImportance {
	param?: string;
	importance?: number;
}
interface PxInteraction {
	param_a?: string;
	param_b?: string;
	score?: number;
}
interface PxConfigCI {
	vector?: PxVector;
	mean?: number;
	se?: number;
	n?: number;
}
interface PxSuggestion {
	vector?: PxVector;
	acq_value?: number;
	predicted_mean?: number;
	predicted_std?: number;
}
interface PxReport {
	objective?: string;
	n_experiments?: number;
	knees?: PxKnee[];
	importance?: PxImportance[];
	interactions?: PxInteraction[];
	best_config?: PxConfigCI;
	indistinguishable_peers?: PxConfigCI[];
	suggested_next?: PxSuggestion[];
}

function vectorStr(v: PxVector | undefined): string {
	if (!v) return "";
	return Object.keys(v).sort().map((k) => `${k}=${v[k]}`).join(" ");
}

function loadParamexp(
	d: string | undefined,
): { report: PxReport; svgs: { name: string; svg: string }[] } | null {
	if (!d) return null;
	let report: PxReport;
	try {
		report = JSON.parse(Deno.readTextFileSync(`${d}/report.json`)) as PxReport;
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		console.warn(`paramexp: no report.json at ${d} (${msg}); ML section omitted`);
		return null;
	}
	const svgs: { name: string; svg: string }[] = [];
	// Preferred order; unknown extras (surface_*, sweep_*) appended after.
	const preferred = ["importance.svg", "sensitivity.svg", "interactions.svg", "contour.svg"];
	const seen = new Set<string>();
	const tryAdd = (fname: string) => {
		if (seen.has(fname)) return;
		try {
			svgs.push({
				name: fname.replace(/\.svg$/, ""),
				svg: Deno.readTextFileSync(`${d}/${fname}`),
			});
			seen.add(fname);
		} catch { /* not present: skip */ }
	};
	for (const f of preferred) tryAdd(f);
	try {
		for (const e of Deno.readDirSync(d)) {
			if (e.isFile && e.name.endsWith(".svg")) tryAdd(e.name);
		}
	} catch { /* dir unreadable: keep whatever we have */ }
	return { report, svgs };
}

function fmt(v: number | undefined, digits = 2): string {
	return v === undefined ? "—" : (+v.toFixed(digits)).toString();
}

function buildDashboard(): string {
	const cap = recs
		.filter((r) => r.group === "capacity" && r.sessions !== undefined)
		.sort((a, b) => (a.sessions ?? 0) - (b.sessions ?? 0));
	const held = cap.filter((r) => (r.verdict ?? "").toUpperCase() === "HOLDS");
	const ceiling = held.length ? Math.max(...held.map((r) => r.sessions ?? 0)) : undefined;
	const px = loadParamexp(paramexpDir);

	const parts: string[] = [];
	parts.push(`<!doctype html><html lang="en"><head><meta charset="utf-8">`);
	parts.push(`<meta name="viewport" content="width=device-width,initial-scale=1">`);
	parts.push(`<title>Relay bench dashboard</title><style>
:root{--fg:#1a1a2e;--muted:#5b5b7a;--bg:#f6f7fb;--card:#fff;--line:#e2e4ee;--accent:#1f77b4;--ok:#2ca02c;--bad:#d62728}
*{box-sizing:border-box}body{margin:0;font:14px/1.5 system-ui,sans-serif;color:var(--fg);background:var(--bg)}
.wrap{max-width:1080px;margin:0 auto;padding:24px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:32px 0 12px;padding-bottom:6px;border-bottom:2px solid var(--line)}
.sub{color:var(--muted);margin:0 0 18px}
.cards{display:flex;flex-wrap:wrap;gap:12px;margin:12px 0}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px 16px;min-width:150px;flex:1 1 150px}
.card .k{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
.card .v{font-size:24px;font-weight:700;margin:4px 0 2px}
.card .n{color:var(--muted);font-size:12px}
.hero{background:linear-gradient(135deg,#1f2a44,#243b6b);color:#fff;border:none}
.hero .k{color:#b9c6e8}.hero .n{color:#c9d4ef}
table{border-collapse:collapse;width:100%;background:var(--card);border:1px solid var(--line);border-radius:10px;overflow:hidden}
th,td{padding:8px 12px;text-align:right;border-bottom:1px solid var(--line)}th:first-child,td:first-child{text-align:left}
th{background:#eef0f7;font-size:12px;text-transform:uppercase;letter-spacing:.03em;color:var(--muted)}
tr:last-child td{border-bottom:none}
.badge{display:inline-block;padding:2px 9px;border-radius:20px;font-size:12px;font-weight:600}
.badge.ok{background:#e6f5e9;color:var(--ok)}.badge.bad{background:#fce8e8;color:var(--bad)}
.plot{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:8px;margin:14px 0;overflow-x:auto}
.plot svg{max-width:100%;height:auto;display:block;margin:0 auto}
.grid2{display:grid;grid-template-columns:repeat(auto-fit,minmax(340px,1fr));gap:14px}
.note{color:var(--muted);font-size:13px;background:#fff8e6;border:1px solid #f0e2b6;border-radius:8px;padding:10px 12px;margin:12px 0}
.muted{color:var(--muted)}code{background:#eceef5;padding:1px 5px;border-radius:4px;font-size:13px}
.bar{height:10px;border-radius:5px;background:var(--accent)}
</style></head><body><div class="wrap">`);

	parts.push(`<h1>Relay benchmark dashboard</h1>`);
	parts.push(
		`<p class="sub">Generated ${esc(new Date().toISOString())} · ${recs.length} records${
			px ? ` · ML: ${px.report.n_experiments ?? "?"} experiments` : ""
		}</p>`,
	);

	// ---- Capacity headline ----
	parts.push(`<h2>Capacity — concurrent sessions</h2>`);
	if (cap.length) {
		parts.push(`<div class="cards">`);
		parts.push(
			`<div class="card hero"><div class="k">Session ceiling (HOLDS)</div><div class="v">${
				ceiling !== undefined ? ceiling.toLocaleString() : "none held"
			}</div><div class="n">highest S with ≥99% receiving</div></div>`,
		);
		const top = cap[cap.length - 1];
		parts.push(
			`<div class="card"><div class="k">Per session</div><div class="v">${
				fmt(top.per_session_kb, 1)
			} KB</div><div class="n">heap at S=${top.sessions?.toLocaleString()}</div></div>`,
		);
		if (top.sessions && top.goros !== undefined) {
			parts.push(
				`<div class="card"><div class="k">Goroutines / session</div><div class="v">${
					fmt(top.goros / top.sessions, 1)
				}</div><div class="n">at S=${top.sessions.toLocaleString()}</div></div>`,
			);
		}
		parts.push(`</div>`);
		parts.push(
			`<table><thead><tr><th>Sessions</th><th>Connected</th><th>Receiving</th><th>Heap MB</th><th>KB/session</th><th>Goros</th><th>Verdict</th></tr></thead><tbody>`,
		);
		for (const r of cap) {
			const ok = (r.verdict ?? "").toUpperCase() === "HOLDS";
			parts.push(
				`<tr><td>${r.sessions?.toLocaleString()}</td><td>${num(r.connected)}</td><td>${
					num(r.receiving)
				}</td><td>${fmt(r.heap_mb, 0)}</td><td>${fmt(r.per_session_kb, 1)}</td><td>${
					num(r.goros)
				}</td><td><span class="badge ${ok ? "ok" : "bad"}">${
					esc(r.verdict ?? "?")
				}</span></td></tr>`,
			);
		}
		parts.push(`</tbody></table>`);
		parts.push(
			`<p class="note">Capacity is measured in-process (clients + relay share one process), so KB/session and goros/session include client-side cost — read the <em>shape</em> across S and the HOLDS/CANNOT-HOLD boundary, not absolutes. WSL2/loaded VMs swing ±10×; rerun on bare-metal Linux for a hard ceiling.</p>`,
		);
	} else {
		parts.push(
			`<p class="muted">No capacity records. Run <code>BenchmarkRelay_ConnectionCarry</code> with <code>BENCH_RESULTS_DIR</code> set (SESSIONS sweep) to populate this section.</p>`,
		);
	}

	// ---- Decision summary ----
	parts.push(`<h2>Decision summary</h2><div class="cards">`);
	if (perHop) {
		parts.push(
			`<div class="card"><div class="k">Per-hop latency</div><div class="v">${
				perHop.slope.toFixed(3)
			} ms</div><div class="n">per relay hop · R²=${perHop.r2.toFixed(3)}</div></div>`,
		);
	}
	parts.push(
		`<div class="card"><div class="k">Fan-out knee</div><div class="v">${
			kneeRec ? "K=" + kneeRec.k : "none"
		}</div><div class="n">first K with loss≥${KNEE_LOSS_PCT}%</div></div>`,
	);
	if (maxK?.p99_ms !== undefined && maxK?.median_ms !== undefined) {
		parts.push(
			`<div class="card"><div class="k">Jitter @ max K</div><div class="v">${
				(maxK.p99_ms - maxK.median_ms).toFixed(2)
			} ms</div><div class="n">p99−median at K=${maxK.k}</div></div>`,
		);
	}
	if (maxK?.k && maxK.heap_mb !== undefined) {
		parts.push(
			`<div class="card"><div class="k">Heap / subscriber</div><div class="v">${
				(maxK.heap_mb / maxK.k).toFixed(2)
			} MB</div><div class="n">at K=${maxK.k}</div></div>`,
		);
	}
	if (maxK?.fairness !== undefined) {
		parts.push(
			`<div class="card"><div class="k">Fairness @ max K</div><div class="v">${
				maxK.fairness.toFixed(3)
			}</div><div class="n">Jain, 1=perfect</div></div>`,
		);
	}
	parts.push(`</div>`);
	if (floored.length > 0) {
		parts.push(
			`<p class="note">⚠ ${floored.length} record(s) have median latency ≈ 0 — timer-floored on fast loopback. Absolute latencies are NOT representative; read p99/tail and shape across K.</p>`,
		);
	}

	// ---- Plots (overview first) ----
	parts.push(`<h2>Benchmark plots</h2>`);
	const ordered = [
		...collected.filter((p) => p.name === "overview"),
		...collected.filter((p) => p.name !== "overview"),
	];
	if (ordered.length) {
		for (const p of ordered) parts.push(`<div class="plot">${p.svg}</div>`);
	} else {
		parts.push(`<p class="muted">No plots (insufficient data).</p>`);
	}

	// ---- ML analysis (paramexp) ----
	parts.push(`<h2>ML analysis — GP surrogate (paramexp)</h2>`);
	if (px) {
		const r = px.report;
		parts.push(
			`<p class="sub">Objective <code>${esc(r.objective ?? "?")}</code> · ${
				r.n_experiments ?? "?"
			} experiments · Gaussian-process response-surface fit.</p>`,
		);
		if (r.best_config?.vector) {
			const bc = r.best_config;
			parts.push(
				`<div class="cards"><div class="card hero"><div class="k">Best config</div><div class="v" style="font-size:16px">${
					esc(vectorStr(bc.vector))
				}</div><div class="n">${fmt(bc.mean, 3)} ± ${fmt(bc.se, 2)} (n=${bc.n ?? "?"})${
					r.indistinguishable_peers?.length
						? ` · ${r.indistinguishable_peers.length} statistically tied`
						: ""
				}</div></div></div>`,
			);
		}
		if (r.importance?.length) {
			const maxImp = Math.max(...r.importance.map((i) => i.importance ?? 0), 1e-9);
			parts.push(
				`<h3 style="font-size:15px;margin:18px 0 8px">Parameter importance (η²)</h3><table><tbody>`,
			);
			for (
				const i of [...r.importance].sort((a, b) =>
					(b.importance ?? 0) - (a.importance ?? 0)
				)
			) {
				const w = Math.round(((i.importance ?? 0) / maxImp) * 100);
				parts.push(
					`<tr><td>${
						esc(i.param ?? "?")
					}</td><td style="width:60%"><div class="bar" style="width:${w}%"></div></td><td>${
						fmt(i.importance, 3)
					}</td></tr>`,
				);
			}
			parts.push(`</tbody></table>`);
		}
		if (r.knees?.length) {
			parts.push(
				`<h3 style="font-size:15px;margin:18px 0 8px">Knees</h3><table><thead><tr><th>Param</th><th>Value</th><th>Score</th></tr></thead><tbody>`,
			);
			for (const k of r.knees) {
				parts.push(
					`<tr><td>${esc(k.param ?? "?")}</td><td>${esc(k.value ?? "?")}</td><td>${
						fmt(k.score, 3)
					}</td></tr>`,
				);
			}
			parts.push(`</tbody></table>`);
		}
		if (r.interactions?.length) {
			parts.push(
				`<h3 style="font-size:15px;margin:18px 0 8px">Interactions</h3><table><thead><tr><th>Pair</th><th>Score</th></tr></thead><tbody>`,
			);
			for (
				const it of [...r.interactions].sort((a, b) => (b.score ?? 0) - (a.score ?? 0))
					.slice(0, 8)
			) {
				parts.push(
					`<tr><td>${esc(it.param_a ?? "?")} × ${esc(it.param_b ?? "?")}</td><td>${
						fmt(it.score, 3)
					}</td></tr>`,
				);
			}
			parts.push(`</tbody></table>`);
		}
		if (r.suggested_next?.length) {
			parts.push(
				`<h3 style="font-size:15px;margin:18px 0 8px">Suggested next measurements</h3><table><thead><tr><th>Config</th><th>Pred. mean</th><th>Pred. std</th><th>Acq.</th></tr></thead><tbody>`,
			);
			for (const s of r.suggested_next) {
				parts.push(
					`<tr><td>${esc(vectorStr(s.vector))}</td><td>${
						fmt(s.predicted_mean, 2)
					}</td><td>${fmt(s.predicted_std, 2)}</td><td>${fmt(s.acq_value, 3)}</td></tr>`,
				);
			}
			parts.push(`</tbody></table>`);
		}
		if (px.svgs.length) {
			parts.push(`<div class="grid2">`);
			for (const s of px.svgs) parts.push(`<div class="plot">${s.svg}</div>`);
			parts.push(`</div>`);
		}
	} else {
		parts.push(
			`<p class="muted">No paramexp report supplied. Pass <code>--paramexp &lt;dir&gt;</code> pointing at a paramexp output dir (with <code>report.json</code>) to fold in knees, η² importance, interactions and suggested-next.</p>`,
		);
	}

	parts.push(`</div></body></html>`);
	return parts.join("\n");
}

Deno.writeTextFileSync(`${dir}/index.html`, buildDashboard());
console.log(`wrote index.html (consolidated dashboard${paramexpDir ? " + paramexp ML" : ""})`);

console.log("done.");
