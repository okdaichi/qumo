// Scenario registry for the demo. One source of truth for each pipeline's
// WebTransport origin port, UI mode, default subscribe path, and (for ingest
// scenarios) the ffmpeg push command shown in the UI. The push commands mirror
// the `mage demo:push` ffmpeg args in magefiles/magefile.go.

export type ScenarioId = "echo" | "rtmp" | "rtsp";
export type ScenarioMode = "publish-subscribe" | "subscribe";

export interface Scenario {
	id: ScenarioId;
	label: string;
	/** WebTransport origin port for this scenario. */
	port: number;
	mode: ScenarioMode;
	/** Explicit default path for this scenario (echo: /echo, ingest: /live/demo). */
	defaultPath: string;
	/** ffmpeg one-liner an external encoder can paste (ingest scenarios). */
	pushCommand?: string;
	/** Human-readable push target URL for the instructions header. */
	pushTarget?: string;
}

const RTMP_PUSH =
	"ffmpeg -re -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440:sample_rate=48000 " +
	"-c:v libx264 -preset veryfast -tune zerolatency -profile:v baseline -g 60 " +
	"-c:a aac -ar 48000 -ac 2 -f flv rtmp://localhost:1935/live/demo";

const RTSP_PUSH =
	"ffmpeg -re -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440:sample_rate=48000 " +
	"-c:v libx264 -preset veryfast -tune zerolatency -profile:v baseline -g 60 " +
	"-c:a aac -ar 48000 -ac 2 -f rtsp -rtsp_transport tcp rtsp://localhost:8554/live/demo";

export const SCENARIOS: Record<ScenarioId, Scenario> = {
	echo: {
		id: "echo",
		label: "Echo",
		port: 4433,
		mode: "publish-subscribe",
		defaultPath: "/echo",
	},
	rtmp: {
		id: "rtmp",
		label: "RTMP ingest",
		port: 4443,
		mode: "subscribe",
		defaultPath: "/live/demo",
		pushCommand: RTMP_PUSH,
		pushTarget: "rtmp://localhost:1935/live/demo",
	},
	rtsp: {
		id: "rtsp",
		label: "RTSP ingest",
		port: 4543,
		mode: "subscribe",
		defaultPath: "/live/demo",
		pushCommand: RTSP_PUSH,
		pushTarget: "rtsp://localhost:8554/live/demo",
	},
};

export const SCENARIO_ORDER: ScenarioId[] = ["echo", "rtmp", "rtsp"];

export function isScenarioId(x: string): x is ScenarioId {
	return x in SCENARIOS;
}

// Each scenario is a distinct WebTransport origin (different port). Derive the
// origin URL from VITE_RELAY_URL's host + the scenario's port.
export function relayUrlFor(id: ScenarioId): string {
	const base = new URL(import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433");
	return `https://${base.hostname}:${SCENARIOS[id].port}`;
}
