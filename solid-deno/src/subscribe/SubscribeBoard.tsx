import { createEffect, createMemo, createSignal, onCleanup, onMount, Show, untrack } from "solid-js";
import { AudioDecodeNode, VideoContext, VideoDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@okdaichi/moq";
import { parseCatalog } from "@okdaichi/moq/msf";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { useBroadcastPath } from "../useBroadcastPath.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";

export function SubscribeBoard(props: { session: Promise<Session> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Reactive decoder config: undefined until the first catalog arrives — no pre-config.
	const [decoderConfig, setDecoderConfig] = createSignal<VideoDecoderConfig | undefined>();

	const broadcastPath = useBroadcastPath();

	let canvasEle: HTMLCanvasElement | undefined;
	let videoContext: VideoContext | undefined;
	let videoDecodeNode: VideoDecodeNode | undefined;
	let audioContext: AudioContext | undefined;
	let audioDecodeNode: AudioDecodeNode | undefined;
	let currentCancel: (() => void) | null = null;

	// Memo: stable fingerprint of active codec parameters.
	// createMemo uses === equality — downstream effects only re-run when the key string actually changes,
	// preventing spurious configure() calls that would reset decoder state and require a key frame.
	const configKey = createMemo((): string => {
		const cfg = decoderConfig();
		if (!cfg) return "";
		let descKey = "";
		if (cfg.description != null) {
			const buf = cfg.description instanceof ArrayBuffer
				? new Uint8Array(cfg.description)
				: new Uint8Array((cfg.description as ArrayBufferView).buffer);
			descKey = btoa(String.fromCharCode(...buf));
		}
		return `${cfg.codec}|${cfg.codedWidth ?? ""}|${cfg.codedHeight ?? ""}|${descKey}`;
	});

	onMount(() => {
		if (canvasEle) {
			videoContext = new VideoContext({ canvas: canvasEle });
			videoDecodeNode = new VideoDecodeNode(videoContext);
			videoDecodeNode.connect(videoContext.destination);
			setCanvasWidth(videoContext.destination.canvas.width);
			setCanvasHeight(videoContext.destination.canvas.height);

			// AudioContext for playback of the subscribed audio track.
			audioContext = new AudioContext();
			audioDecodeNode = new AudioDecodeNode(audioContext);
			audioDecodeNode.connect(audioContext.destination);
		}
	});

	// Effect depends only on the memo — skipped entirely when configKey() returns the same string.
	// decoderConfig() is read via untrack so it doesn't add a separate dependency.
	createEffect(() => {
		if (!configKey()) return;
		const cfg = untrack(decoderConfig)!;
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
			// Resolved the moment the first catalog group is received.
			let resolveFirstConfig!: () => void;
			const firstConfigReady = new Promise<void>((r) => { resolveFirstConfig = r; });
			let firstConfigReceived = false;

			// Subscribe to catalog: each new group updates the reactive signal,
			// which triggers createEffect → videoDecodeNode.configure() automatically.
			session.subscribe(broadcastPath, "catalog").then(
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
								};
								setDecoderConfig(cfg); // → createEffect → videoDecodeNode.configure()
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
								// Wait for the first catalog before feeding any frames.
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

			// Subscribe to audio — gracefully skipped if the publisher has no audio track.
			session.subscribe(broadcastPath, "audio").then(
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
		setDecoderConfig(undefined); // reset for next subscription
		audioContext?.suspend().catch(() => {});
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
