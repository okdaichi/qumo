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
	// Signal for the latest VideoMetadata received over the video.meta MoQ track.
	const [decoderConfig, setDecoderConfig] = createSignal<VideoMetadata | undefined>();

	const broadcastPath = useBroadcastPath();

	let canvasEle: HTMLCanvasElement | undefined;
	let videoContext: VideoContext | undefined;
	let videoDecodeNode: VideoDecodeNode | undefined;

	// Track current cancel function for cleanup
	let currentCancel: (() => void) | null = null;

	onMount(() => {
		if (canvasEle) {
			videoContext = new VideoContext({ canvas: canvasEle });
			videoDecodeNode = new VideoDecodeNode(videoContext);

			// Connect VideoDecodeNode to destination
			videoDecodeNode.connect(videoContext.destination);

			// Set canvas size from VideoContext
			setCanvasWidth(videoContext.destination.canvas.width);
			setCanvasHeight(videoContext.destination.canvas.height);
		}
	});

	onCleanup(() => {
		stopSubscribing();
	});

	// MoQ reactive: whenever video.meta delivers new codec params, reconfigure the decoder
	// through the SolidJS reactive graph instead of calling configure() imperatively.
	createEffect(() => {
		const config = decoderConfig();
		if (config) {
			videoDecodeNode?.configure(config);
			console.log("[Subscribe] VideoDecoder reactively configured:", config.codec);
		}
	});

	const startSubscribing = async () => {
		// Create fresh context for each subscription
		const [ctx, cancel] = withCancel(background());
		currentCancel = cancel;

		try {
			setError(null);

			if (!videoContext || !videoDecodeNode) {
				throw new Error("Video context not initialized");
			}

			const session = await props.session;

			// Configure decoder immediately with a known-good default so frames
			// are never fed into an unconfigured VideoDecoder.  video.meta will
			// reconfigure with exact publisher settings when it arrives.
			const defaultDecoderConfig: VideoMetadata = {
				codec: "vp09.00.10.08",
				codedWidth: canvasWidth(),
				codedHeight: canvasHeight(),
				startGroup: 0,
			};
			// Signal update triggers createEffect → videoDecodeNode.configure() reactively.
			setDecoderConfig(defaultDecoderConfig);

			// Subscribe to video.meta: loop on acceptGroup so every new group
			// (e.g. encoder reconfigure, resolution change) reconfigures the decoder.
			session.subscribe(broadcastPath, "video.meta").then(
				async ([videoMetaTrack, videoMetaErr]) => {
					if (videoMetaErr) {
						if (!isSubscribed()) return; // expected during shutdown
						console.warn("[Subscribe] video.meta subscribe failed:", videoMetaErr);
						return;
					}

					// Loop: receive every video.meta group published by the encoder.
					// Each new group calls setDecoderConfig → createEffect → decoder.configure().
					while (isSubscribed()) {
						const [group, groupErr] = await videoMetaTrack.acceptGroup(ctx.done());
						if (groupErr) {
							if (!isSubscribed()) break; // expected during shutdown
							console.warn("[Subscribe] video.meta acceptGroup:", groupErr);
							break;
						}

						for await (const frame of group.frames()) {
							const meta = JSON.parse(new TextDecoder().decode(frame.bytes)) as VideoMetadata;
							setDecoderConfig(meta); // → createEffect → videoDecodeNode.configure(meta)
							console.log("[Subscribe] video.meta group received, codec:", meta.codec);
						}
					}
				},
			);

			session.subscribe(broadcastPath, "video").then(
				([videoTrack, videoErr]) => {
					if (videoErr) {
						if (!isSubscribed()) return; // expected during shutdown
						console.warn("[Subscribe] video subscribe failed:", videoErr);
						return;
					}

					const videoStream = new ReadableStream<EncodedVideoChunk>({
						async start(controller) {
							try {
								while (isSubscribed()) {
									const [group, groupErr] = await videoTrack.acceptGroup(
										ctx.done(),
									);
									if (groupErr) {
										if (!isSubscribed()) break; // expected during shutdown
										console.error(
											"moq: Error accepting video group:",
											groupErr,
										);
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

					// Decode from stream (decoder already pre-configured with default VP9)
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
