import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { VideoContext, VideoDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@okdaichi/moq";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { useBroadcastPath } from "../useBroadcastPath.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";
import type { VideoMetadata } from "../metadata/mod.ts";

export function SubscribeBoard(props: { session: Promise<Session> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Reactive decoder config: undefined until the first video.meta arrives — no pre-config.
	const [decoderConfig, setDecoderConfig] = createSignal<VideoMetadata | undefined>();

	const broadcastPath = useBroadcastPath();

	let canvasEle: HTMLCanvasElement | undefined;
	let videoContext: VideoContext | undefined;
	let videoDecodeNode: VideoDecodeNode | undefined;
	let currentCancel: (() => void) | null = null;
	// Tracks the last applied config key so configure() is skipped when nothing changed.
	// Re-configuring on every video.meta (every GOP) resets decoder state and requires a key frame.
	let lastConfigKey = "";

	const configKey = (cfg: VideoMetadata): string => {
		let descKey = "";
		if (cfg.description != null) {
			const buf = cfg.description instanceof ArrayBuffer
				? new Uint8Array(cfg.description)
				: new Uint8Array((cfg.description as ArrayBufferView).buffer);
			descKey = btoa(String.fromCharCode(...buf));
		}
		return `${cfg.codec}|${cfg.codedWidth ?? ""}|${cfg.codedHeight ?? ""}|${descKey}`;
	};

	onMount(() => {
		if (canvasEle) {
			videoContext = new VideoContext({ canvas: canvasEle });
			videoDecodeNode = new VideoDecodeNode(videoContext);
			videoDecodeNode.connect(videoContext.destination);
			setCanvasWidth(videoContext.destination.canvas.width);
			setCanvasHeight(videoContext.destination.canvas.height);
		}
	});

	// Reactively configure the decoder only when codec params actually change.
	// Calling configure() on every video.meta (every GOP with unchanged params) resets
	// decoder state and demands a key frame, causing "key frame required" errors on delta frames.
	createEffect(() => {
		const cfg = decoderConfig();
		if (!cfg) return;
		const key = configKey(cfg);
		if (key === lastConfigKey) return; // params unchanged — skip
		lastConfigKey = key;
		videoDecodeNode?.configure(cfg);
		console.log("[Subscribe] VideoDecoder configured:", cfg.codec);
	});

	onCleanup(() => {
		stopSubscribing();
	});

	const startSubscribing = async () => {
		const [ctx, cancel] = withCancel(background());
		currentCancel = cancel;

		try {
			setError(null);

			if (!videoContext || !videoDecodeNode) {
				throw new Error("Video context not initialized");
			}

			const session = await props.session;

			// Gate: the video loop awaits this before feeding any frames to the decoder.
			// Resolved the moment the first video.meta group is fully received.
			let resolveFirstConfig!: () => void;
			const firstConfigReady = new Promise<void>((r) => { resolveFirstConfig = r; });
			let firstConfigReceived = false;

			// Subscribe to video.meta: each new group updates the reactive signal,
			// which triggers createEffect → videoDecodeNode.configure() automatically.
			session.subscribe(broadcastPath, "video.meta").then(
				async ([videoMetaTrack, videoMetaErr]) => {
					if (videoMetaErr) {
						if (!isSubscribed()) return;
						console.warn("[Subscribe] video.meta subscribe failed:", videoMetaErr);
						return;
					}

					while (isSubscribed()) {
						const [group, groupErr] = await videoMetaTrack.acceptGroup(ctx.done());
						if (groupErr) {
							if (!isSubscribed()) break;
							console.warn("[Subscribe] video.meta acceptGroup:", groupErr);
							break;
						}

						for await (const frame of group.frames()) {
							const { data } = deserializeMediaFrame(frame.bytes);
							const meta = JSON.parse(new TextDecoder().decode(data)) as VideoMetadata;
							setDecoderConfig(meta); // → createEffect → videoDecodeNode.configure()
							console.log("[Subscribe] video.meta received, codec:", meta.codec);
							if (!firstConfigReceived) {
								firstConfigReceived = true;
								resolveFirstConfig();
							}
						}
					}
				},
			);

			session.subscribe(broadcastPath, "video").then(
				([videoTrack, videoErr]) => {
					if (videoErr) {
						if (!isSubscribed()) return;
						console.warn("[Subscribe] video subscribe failed:", videoErr);
						return;
					}

					const videoStream = new ReadableStream<EncodedVideoChunk>({
						async start(controller) {
							try {
								// Wait for the first video.meta before feeding any frames.
								await firstConfigReady;

								while (isSubscribed()) {
									const [group, groupErr] = await videoTrack.acceptGroup(ctx.done());
									if (groupErr) {
										if (!isSubscribed()) break;
										console.error("moq: Error accepting video group:", groupErr);
										break;
									}

									let isKey = true;
									for await (const frame of group.frames()) {
										const { timestamp, data } = deserializeMediaFrame(frame.bytes);
										const chunk = new EncodedVideoChunk({
											type: isKey ? "key" : "delta",
											timestamp,
											data,
										});
										controller.enqueue(chunk);
										isKey = false;
									}
								}
								controller.close();
							} catch (err) {
								if (isSubscribed()) {
									console.error("Video track error:", err);
									controller.error(err);
								} else {
									controller.close();
								}
							} finally {
								videoTrack.closeWithError(SubscribeErrorCode.InternalError);
							}
						},
					});

					videoDecodeNode?.decodeFrom(videoStream);
				},
			);

			setIsSubscribed(true);
		} catch (err) {
			const errorMessage = err instanceof Error ? err.message : String(err);
			setError(errorMessage);
			console.error("Failed to start subscribing:", err);
			setIsSubscribed(false);
		}
	};

	const stopSubscribing = () => {
		if (currentCancel) {
			currentCancel();
			currentCancel = null;
		}
		setDecoderConfig(undefined); // reset for next subscription
		lastConfigKey = "";
		setIsSubscribed(false);
		console.log("Stopped subscribing");
	};

	return (
		<div class="subscribe-board">
			<h2>Subscribe Board</h2>

			<div class="controls">
				<div class="path-input">
					<label>Broadcast Path:</label>
					<span>{broadcastPath}</span>
				</div>

				<div class="stream-controls">
					<Show
						when={!isSubscribed()}
						fallback={
							<button type="button" onClick={stopSubscribing} class="btn-stop">
								Stop Subscribing
							</button>
						}
					>
						<button type="button" onClick={startSubscribing} class="btn-start">
							Start Subscribing
						</button>
					</Show>
				</div>
			</div>

			<Show when={error()}>
				<div class="error-message">
					Error: {error()}
				</div>
			</Show>

			<Show when={isSubscribed()}>
				<div class="status-message">
					Subscribing to: {broadcastPath}
				</div>
			</Show>

			<div class="video-preview">
				<canvas
					ref={canvasEle}
					width={canvasWidth()}
					height={canvasHeight()}
					style={{
						width: "100%",
						"max-width": "800px",
						height: "auto",
						border: "1px solid #ccc",
						"border-radius": "8px",
						background: "#000",
					}}
				/>
			</div>
		</div>
	);
}
