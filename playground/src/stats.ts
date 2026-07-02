import { createSignal } from "solid-js";

export interface StatsSnapshot {
	/** Frames processed in the last window (per-second at the default 1s). */
	fps: number;
	/** Media bitrate over the last window, in Mbps. */
	bitrateMbps: number;
}

// A rolling rate meter for media frames + bytes — the numbers a streaming demo
// needs to "feel" the pipeline. Call `mark(byteLen)` per processed frame, read
// `stats()` reactively, and `start()` / `stop()` the window ticker. `onTick`
// runs after each window rolls so callers can refresh derived signals (encoder/
// decoder queue depth, RTT, …) on the same cadence.
export function createStatsTicker(intervalMs = 1000, onTick?: () => void) {
	let frames = 0;
	let bytes = 0;
	const [stats, setStats] = createSignal<StatsSnapshot>({ fps: 0, bitrateMbps: 0 });
	let timer: ReturnType<typeof setInterval> | undefined;

	const roll = () => {
		const secs = intervalMs / 1000;
		setStats({
			fps: Math.round(frames / secs),
			bitrateMbps: Number(((bytes * 8) / secs / 1_000_000).toFixed(2)),
		});
		frames = 0;
		bytes = 0;
		onTick?.();
	};

	return {
		stats,
		mark(byteLen: number) {
			frames++;
			bytes += byteLen;
		},
		start() {
			if (timer) return;
			frames = 0;
			bytes = 0;
			setStats({ fps: 0, bitrateMbps: 0 });
			timer = setInterval(roll, intervalMs);
		},
		stop() {
			if (timer) {
				clearInterval(timer);
				timer = undefined;
			}
			setStats({ fps: 0, bitrateMbps: 0 });
		},
	};
}
