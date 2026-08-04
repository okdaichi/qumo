import { createSignal, onCleanup, onMount, Show } from "solid-js";
import type { Accessor } from "solid-js";
import Hls from "hls.js";

// HlsPlayer plays the HLS egress for a track. The egress (`qumo hls`) is a
// separate process that subscribes to the relay and serves HLS; its base URL
// comes from VITE_HLS_URL (default http://localhost:8081 — it must differ from
// the playground's own web UI port).
export function HlsPlayer(props: { path: Accessor<string> }) {
	const base = (import.meta.env.VITE_HLS_URL as string | undefined) ?? "http://localhost:8081";
	const playlistUrl = () => `${base}/${props.path()}/playlist.m3u8`;

	let video!: HTMLVideoElement;
	let hls: Hls | undefined;
	const [status, setStatus] = createSignal<string>("loading");

	onMount(() => {
		const url = playlistUrl();
		// Safari plays HLS natively; others need hls.js (MSE).
		if (video.canPlayType("application/vnd.apple.mpegurl")) {
			video.src = url;
			setStatus("native");
		} else if (Hls.isSupported()) {
			hls = new Hls({ liveDurationInfinity: true, lowLatencyMode: true });
			hls.loadSource(url);
			hls.attachMedia(video);
			hls.on(Hls.Events.MANIFEST_PARSED, () => setStatus("ready"));
			hls.on(Hls.Events.ERROR, (_event, data) => {
				if (data.fatal) setStatus(`error: ${data.type}`);
			});
		} else {
			setStatus("unsupported");
		}
	});

	onCleanup(() => hls?.destroy());

	return (
		<div class="subscribe-board">
			<h2>HLS Player</h2>
			<p class="status-message">
				playlist: <code>{playlistUrl()}</code>
			</p>
			<video
				ref={video}
				controls
				autoplay
				muted
				playsinline
				style={{
					width: "100%",
					"max-width": "1280px",
					border: "1px solid #ccc",
					"border-radius": "8px",
					background: "#000",
				}}
			/>
			<Show when={status() !== "ready" && status() !== "native"}>
				<p class="status-message">HLS: {status()}</p>
			</Show>
		</div>
	);
}
