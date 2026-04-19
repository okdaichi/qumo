import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { AudioDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@okdaichi/moq";
import { parseCatalog } from "@okdaichi/moq/msf";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";
import type { BroadcastPath } from "@okdaichi/moq";

export function SubscribeBoard(props: { session: Promise<Session> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);

	// Editable broadcast path — defaults to /live/demo for RTMP ingest demo.
	const [broadcastPathInput, setBroadcastPathInput] = createSignal("/live/demo");
	const broadcastPath = (): BroadcastPath => broadcastPathInput() as BroadcastPath;

	let canvasEle: HTMLCanvasElement | undefined;
	let canvasCtx: CanvasRenderingContext2D | undefined;
	let videoDecoder: VideoDecoder | undefined;
	let audioContext: AudioContext | undefined;
	let audioDecodeNode: AudioDecodeNode | undefined;
	let currentCancel: (() => void) | null = null;



	onMount(() => {
		if (canvasEle) {
			canvasCtx = canvasEle.getContext("2d") ?? undefined;

			// AudioContext for playback of the subscribed audio track.
			audioContext = new AudioContext();
			audioDecodeNode = new AudioDecodeNode(audioContext);
			audioDecodeNode.connect(audioContext.destination);
		}
	});



	onCleanup(() => {
		stopSubscribing();
	});

	const startSubscribing = async () => {
		const [ctx, cancel] = withCancel(background());
		currentCancel = cancel;

		try {
			setError(null);

			if (!canvasCtx || !canvasEle) {
				throw new Error("Canvas not initialized");
			}

			const session = await props.session;
			const subscribePath = broadcastPath(); // snapshot at subscription start

			// Gate: the video loop awaits this before feeding any frames to the decoder.
			// Resolved the moment the first catalog group is received.
			let resolveFirstConfig!: () => void;
			const firstConfigReady = new Promise<void>((r) => { resolveFirstConfig = r; });
			let firstConfigReceived = false;

			// Subscribe to catalog: each new group updates the reactive signal,
			// which triggers createEffect → videoDecodeNode.configure() automatically.
			session.subscribe(subscribePath, "catalog").then(
				async ([catalogTrack, catalogErr]) => {
					if (catalogErr) {
						if (!isSubscribed()) return;
						console.warn("[Subscribe] catalog subscribe failed:", catalogErr);
						return;
					}

					while (isSubscribed()) {
						const [group, groupErr] = await catalogTrack.acceptGroup(ctx.done());
						if (groupErr) {
							if (!isSubscribed()) break;
							console.warn("[Subscribe] catalog acceptGroup:", groupErr);
							break;
						}

						for await (const frame of group.frames()) {
							const catalog = parseCatalog(frame.bytes);
							const videoTrack = catalog.tracks?.find((t) => t.role === "video");
							if (videoTrack?.codec) {
								const cfg: VideoDecoderConfig = {
									codec: videoTrack.codec,
									codedWidth: videoTrack.width,
									codedHeight: videoTrack.height,
									hardwareAcceleration: "prefer-software",
									optimizeForLatency: true,
								};
								// Create / configure VideoDecoder directly (no av-nodes wrapper).
								if (!videoDecoder || videoDecoder.state === "closed") {
									videoDecoder = new VideoDecoder({
										output: (frame: VideoFrame) => {
											if (canvasCtx && canvasEle) {
												canvasCtx.drawImage(frame, 0, 0, canvasEle.width, canvasEle.height);
											}
											frame.close();
										},
										error: (e: DOMException) => {
											console.error("[Subscribe] VideoDecoder error:", e);
										},
									});
								}
								videoDecoder.configure(cfg);
								console.log("[Subscribe] VideoDecoder configured:", cfg.codec, "state:", videoDecoder.state);
								// Update canvas bitmap dimensions to match the actual video so portrait
								// (or any non-default-aspect) streams are not stretched.
								if (videoTrack.width) setCanvasWidth(videoTrack.width);
								if (videoTrack.height) setCanvasHeight(videoTrack.height);
								console.log("[Subscribe] catalog received, video codec:", videoTrack.codec);
								if (!firstConfigReceived) {
									firstConfigReceived = true;
									resolveFirstConfig();
								}
							}
							const audioTrack = catalog.tracks?.find((t) => t.role === "audio");
							if (audioTrack?.codec && audioDecodeNode) {
								audioDecodeNode.configure({
									codec: audioTrack.codec,
									sampleRate: audioTrack.samplerate ?? 48000,
									numberOfChannels: parseInt(audioTrack.channelConfig ?? "2", 10),
								});
								console.log("[Subscribe] audio decoder configured:", audioTrack.codec);
							}
						}
					}
				},
			);

			session.subscribe(subscribePath, "video").then(
				async ([videoTrack, videoErr]) => {
					if (videoErr) {
						if (!isSubscribed()) return;
						console.warn("[Subscribe] video subscribe failed:", videoErr);
						return;
					}

					try {
						// Wait for the first catalog before feeding any frames.
						await firstConfigReady;
						console.log("[Subscribe] video: catalog ready, starting decode loop");

						let frameCount = 0;
						while (isSubscribed()) {
							const [group, groupErr] = await videoTrack.acceptGroup(ctx.done());
							if (groupErr) {
								if (!isSubscribed()) break;
								console.error("[Subscribe] video acceptGroup error:", groupErr);
								break;
							}

							let isKey = true;
							for await (const frame of group.frames()) {
								const { timestamp, data } = deserializeMediaFrame(frame.bytes);

								// Log first 10 frames for diagnostics.
								if (frameCount < 10) {
									const hex = Array.from(data.slice(0, 48))
										.map((b: number) => b.toString(16).padStart(2, "0"))
										.join(" ");
									console.log(
										`[Subscribe] video #${frameCount}: type=${isKey ? "key" : "delta"} ts=${timestamp} len=${data.byteLength} hex=[${hex}]`,
									);
									frameCount++;
								}

								if (!videoDecoder || videoDecoder.state !== "configured") {
									console.warn("[Subscribe] decoder not ready, state:", videoDecoder?.state);
									isKey = false;
									continue;
								}

								// Drop frames if decoder is stalled — prevents infinite loop.
								if (videoDecoder.decodeQueueSize > 10) {
									if (frameCount <= 10) {
										console.warn(`[Subscribe] dropping frame, queue: ${videoDecoder.decodeQueueSize}`);
									}
									isKey = false;
									continue;
								}

								const chunk = new EncodedVideoChunk({
									type: isKey ? "key" : "delta",
									timestamp,
									data,
								});
								videoDecoder.decode(chunk);
								isKey = false;
							}
						}
					} catch (err) {
						if (isSubscribed()) {
							console.error("[Subscribe] video track error:", err);
						}
					} finally {
						videoTrack.closeWithError(SubscribeErrorCode.InternalError);
					}
				},
			);

			// Subscribe to audio — gracefully skipped if the publisher has no audio track.
			session.subscribe(subscribePath, "audio").then(
				async ([audioMoqTrack, audioMoqErr]) => {
					if (audioMoqErr) {
						if (!isSubscribed()) return;
						console.warn("[Subscribe] audio subscribe failed:", audioMoqErr);
						return;
					}
					if (!audioDecodeNode || !audioContext) return;

					// Resume audio context — we are in a user-gesture-triggered async chain.
					await audioContext.resume();

					const audioStream = new ReadableStream<EncodedAudioChunk>({
						async start(controller) {
							try {
								await firstConfigReady;
								while (isSubscribed()) {
									const [group, groupErr] = await audioMoqTrack.acceptGroup(ctx.done());
									if (groupErr) {
										if (!isSubscribed()) break;
										console.error("moq: Error accepting audio group:", groupErr);
										break;
									}
									for await (const frame of group.frames()) {
										const { timestamp, data } = deserializeMediaFrame(frame.bytes);
										const chunk = new EncodedAudioChunk({
											type: "key", // Opus frames are all independently decodable
											timestamp,
											data,
										});
										controller.enqueue(chunk);
									}
								}
								controller.close();
							} catch (err) {
								if (isSubscribed()) {
									console.error("Audio track error:", err);
									controller.error(err);
								} else {
									controller.close();
								}
							} finally {
								audioMoqTrack.closeWithError(SubscribeErrorCode.InternalError);
							}
						},
					});

					audioDecodeNode.decodeFrom(audioStream);
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
		if (videoDecoder && videoDecoder.state !== "closed") {
			videoDecoder.close();
		}
		videoDecoder = undefined;
		audioContext?.suspend().catch(() => {});
		setIsSubscribed(false);
	};

	return (
		<div class="subscribe-board">
			<h2>Subscribe Board</h2>

			<div class="controls">
				<div class="path-input">
					<label>Broadcast Path:</label>
					<input
						type="text"
						value={broadcastPathInput()}
						onInput={(e) => setBroadcastPathInput(e.currentTarget.value)}
						disabled={isSubscribed()}
						placeholder="/live/demo"
						style={{ "font-family": "monospace", padding: "4px 8px" }}
					/>
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
					Subscribing to: {broadcastPath()}
				</div>
			</Show>

			<div class="video-preview">
				<canvas
					ref={canvasEle}
					width={canvasWidth()}
					height={canvasHeight()}
					style={{
						display: "block",
						width: "100%",
						"max-width": `${canvasWidth()}px`,
						"aspect-ratio": `${canvasWidth()} / ${canvasHeight()}`,
						border: "1px solid #ccc",
						"border-radius": "8px",
						background: "#000",
					}}
				/>
			</div>
		</div>
	);
}
