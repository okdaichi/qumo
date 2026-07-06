// Scenario registry for the demo. One source of truth for each pipeline's
// WebTransport origin port, UI mode, and (for ingest scenarios) the push
// scheme/port used to build the ffmpeg command shown in the UI.

export type ScenarioId = "echo" | "rtmp" | "rtsp" | "camera";
export type ScenarioMode = "publish-subscribe" | "subscribe";

export interface Scenario {
	id: ScenarioId;
	label: string;
	/** One-line description shown below the scenario picker. */
	description: string;
	/** WebTransport origin port for this scenario. */
	port: number;
	mode: ScenarioMode;
	/** Ingest-only: scheme + port an external encoder pushes to. */
	pushScheme?: "rtmp" | "rtsp";
	pushPort?: number;
}

export const SCENARIOS: Record<ScenarioId, Scenario> = {
	echo: {
		id: "echo",
		label: "Echo",
		description: "Publish and subscribe from your browser — full round-trip over MoQ.",
		port: 4433,
		mode: "publish-subscribe",
	},
	rtmp: {
		id: "rtmp",
		label: "RTMP",
		description: "Push an RTMP stream from ffmpeg and subscribe in the browser.",
		port: 4443,
		mode: "subscribe",
		pushScheme: "rtmp",
		pushPort: 1935,
	},
	rtsp: {
		id: "rtsp",
		label: "RTSP",
		description: "Push an RTSP stream from ffmpeg and subscribe in the browser.",
		port: 4543,
		mode: "subscribe",
		pushScheme: "rtsp",
		pushPort: 8554,
	},
	camera: {
		id: "camera",
		label: "Camera",
		description: "Pull a live stream from an RTSP IP camera directly into MoQ.",
		port: 4543,
		mode: "subscribe",
	},
};

export const SCENARIO_ORDER: ScenarioId[] = ["echo", "rtmp", "rtsp", "camera"];

export function isScenarioId(x: string): x is ScenarioId {
	return x in SCENARIOS;
}

// Hostname the ingest origins are reachable on. Defaults to the VITE_RELAY_URL
// host (localhost in dev; the public domain for a deployed playground).
export function ingestHost(): string {
	const base = new URL(import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433");
	return base.hostname;
}

// Each scenario is a distinct WebTransport origin (different port). Derive the
// origin URL from VITE_RELAY_URL's host + the scenario's port.
export function relayUrlFor(id: ScenarioId): string {
	const base = new URL(import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433");
	return `https://${base.hostname}:${SCENARIOS[id].port}`;
}

// ffmpeg source pipeline shared by the RTMP/RTSP push instructions.
const FFMPEG_PIPELINE =
	"ffmpeg -re -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440:sample_rate=48000 " +
	"-c:v libx264 -preset veryfast -tune zerolatency -profile:v baseline -g 60 -c:a aac -ar 48000 -ac 2";

// The push target URL for an ingest scenario, embedding the (unique) path so an
// external encoder and the subscriber always agree on the stream.
export function pushTargetFor(id: ScenarioId, path: string): string {
	const s = SCENARIOS[id];
	if (!s.pushScheme || !s.pushPort) return "";
	return `${s.pushScheme}://${ingestHost()}:${s.pushPort}${path}`;
}

// Full copy-pasteable ffmpeg command that pushes to the given path.
export function pushCommandFor(id: ScenarioId, path: string): string {
	const s = SCENARIOS[id];
	if (!s.pushScheme) return "";
	const out = s.pushScheme === "rtmp" ? "-f flv" : "-f rtsp -rtsp_transport tcp";
	const target = pushTargetFor(id, path);
	return `${FFMPEG_PIPELINE} ${out} ${target}`;
}
