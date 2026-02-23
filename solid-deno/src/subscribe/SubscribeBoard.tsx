import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { VideoContext, VideoDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@okdaichi/moq";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { useBroadcastPath } from "../useBroadcastPath.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";
// import type { VideoMetadata } from "../metadata/mod.ts";

export function SubscribeBoard(props: { session: Promise<Session> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);

	const broadcastPath = useBroadcastPath();

	let canvasEle: HTMLCanvasElement | undefined;
	let videoContext: VideoContext | undefined;
	let videoDecodeNode: VideoDecodeNode | undefined;
	// let audioDecodeNode: AudioDecodeNode | undefined;

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

			// Accept announce to get broadcast path
			const [announced, annErr] = await session.acceptAnnounce("/");
			if (annErr) {
				throw annErr;
			}

			// Wait until we receive an announcement that is our own broadcast path as an ACK
			while (true) {
				const [announcement, err] = await announced.receive(new Promise(() => {}));
				if (err) {
					throw err;
				}

				if (announcement.broadcastPath === broadcastPath) {
					break;
				}
			}

			// Subscribe to video metadata track
			// TODO: Re-enable when metadata track is properly implemented on publisher side
			/*
			session.subscribe(broadcastPath, "video.meta").then(
				async ([videoMetaTrack, videoMetaErr]) => {
					if (videoMetaErr) {
						throw videoMetaErr;
					}

					const [group, err] = await videoMetaTrack.acceptGroup(ctx.done());
					if (err) {
						throw err;
					}

					await group.readFrame((frame) => {
						const meta = JSON.parse(new TextDecoder().decode(frame)) as VideoMetadata;
						videoDecodeNode?.configure(meta);
					});
				},
			);
			*/

			// Subscribe to video track
			console.log("[Subscribe] Subscribing to video track...");
			session.subscribe(broadcastPath, "video").then(
				([videoTrack, videoErr]) => {
					if (videoErr) {
						throw videoErr;
					}
					setIsSubscribed(true);
					// const audioTrack = await session.subscribe(broadcastPath, "audio");

					// Create TransformStream to convert track data to EncodedVideoChunk stream
					const videoStream = new ReadableStream<EncodedVideoChunk>({
						async start(controller) {
							try {
								let groupCount = 0;
								while (isSubscribed()) {
									const [group, groupErr] = await videoTrack.acceptGroup(
										ctx.done(),
									);
									if (groupErr) {
										console.error(
											"moq: Error accepting video group:",
											groupErr,
										);
										break;
									}
									groupCount++;

									let isKey = true;
									// let frameCount = 0;

									while (true) {
										const frameErr = await group.readFrame((frame) => {
											// Deserialize MediaFrame
											const { timestamp, data } = deserializeMediaFrame(
												frame,
											);

											// Create EncodedVideoChunk
											// First frame of Group is key, rest are delta
											const chunk = new EncodedVideoChunk({
												type: isKey ? "key" : "delta",
												timestamp,
												data,
											});

											controller.enqueue(chunk);
											isKey = false;
										});
										if (frameErr) {
											console.log(`moq: readFrame done`, "err:", frameErr);
											break;
										}
									}
								}
								controller.close();
							} catch (err) {
								if (isSubscribed()) {
									console.error("Video track error:", err);
									controller.error(err);
								}
							} finally {
								videoTrack.closeWithError(SubscribeErrorCode.InternalError);
								console.log("[Subscribe] video track closed");
								setIsSubscribed(false);
							}
						},
					});

					// Decode from stream
					videoDecodeNode?.decodeFrom(videoStream);
				},
			);
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

	// Auto-reconfigure when canvas size changes
	createEffect(() => {
		const width = canvasWidth();
		const height = canvasHeight();

		if (videoDecodeNode && width > 0 && height > 0) {
			// Configure VideoDecodeNode with hardcoded codec info (VP9)
			// TODO: Need mechanism to receive resolution info from publisher
			videoDecodeNode.configure({
				codec: "vp09.00.10.08",
				codedWidth: width,
				codedHeight: height,
			});
			console.log(`Video decoder configured for ${width}x${height}`);
		}
	});

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
