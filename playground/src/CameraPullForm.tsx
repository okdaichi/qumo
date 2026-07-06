import { type Accessor, createSignal, onCleanup, Show } from "solid-js";

export type PullState = "idle" | "connecting" | "active" | "error";

export function CameraPullForm(props: {
	path: Accessor<string>;
	onStateChange?: (state: PullState) => void;
}) {
	const [url, setUrl] = createSignal("");
	const [state, setState] = createSignal<PullState>("idle");
	const [error, setError] = createSignal<string | null>(null);

	// Stop the pull when the form unmounts (e.g. scenario switch), so the
	// server-side pull doesn't leak and block the next start with a 409.
	onCleanup(() => {
		if (state() === "active" || state() === "connecting") {
			void fetch("/api/pull/stop", { method: "POST" }).catch(() => {});
		}
	});

	const updateState = (s: PullState) => {
		setState(s);
		props.onStateChange?.(s);
	};

	const startPull = async () => {
		const cameraUrl = url().trim();
		if (!cameraUrl) return;

		updateState("connecting");
		setError(null);

		try {
			const resp = await fetch("/api/pull", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ url: cameraUrl, path: props.path() }),
			});
			if (!resp.ok) {
				const body = await resp.text();
				throw new Error(body || `HTTP ${resp.status}`);
			}
			updateState("active");
		} catch (e) {
			setError(e instanceof Error ? e.message : String(e));
			updateState("error");
		}
	};

	const stopPull = async () => {
		try {
			await fetch("/api/pull/stop", { method: "POST" });
		} catch (_) {
			/* ignore */
		}
		updateState("idle");
	};

	return (
		<div class="camera-pull-form">
			<label for="camera-url">RTSP camera URL</label>
			<input
				id="camera-url"
				class="camera-pull-input"
				type="text"
				placeholder="rtsp://user:pass@192.168.1.100/stream"
				value={url()}
				onInput={(e) => setUrl(e.currentTarget.value)}
				disabled={state() === "connecting" || state() === "active"}
			/>
			<Show when={state() === "idle" || state() === "error"}>
				<button
					type="button"
					class="btn-start camera-pull-actions"
					onClick={startPull}
					disabled={!url().trim()}
				>
					Start Pull
				</button>
			</Show>
			<Show when={state() === "connecting"}>
				<button type="button" class="btn-stop camera-pull-actions" disabled>
					Connecting…
				</button>
			</Show>
			<Show when={state() === "active"}>
				<button
					type="button"
					class="btn-stop camera-pull-actions"
					onClick={stopPull}
				>
					Stop Pull
				</button>
			</Show>
			<Show when={state() !== "idle"}>
				<div class="camera-pull-status" data-state={state()}>
					{state() === "connecting" && "Connecting to camera…"}
					{state() === "active" && `Streaming from ${url()}`}
					{state() === "error" && (error() ?? "Failed to connect")}
				</div>
			</Show>
		</div>
	);
}
