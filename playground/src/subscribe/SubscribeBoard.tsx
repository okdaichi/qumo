import { type Accessor, createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { AudioDecodeNode, VideoContext, VideoDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@qumo/moq";
import { parseCatalog } from "@qumo/moq/msf";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";
import type { BroadcastPath } from "@qumo/moq";

export function SubscribeBoard(props: { session: Promise<Session>; path: Accessor<string> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Viewer controls (#136): pure client-side, no timeline (MoQ is live-only).
	const [volume, setVolume] = createSignal(1);
	const [muted, setMuted] = createSignal(false);
	const [isFullscreen, setIsFullscreen] = createSignal(false);

	let canvasEle: HTMLCanvasElement | undefined;
	let previewEle: HTMLDivElement | undefined;
	let videoContext: VideoContext | undefined;
	let videoDecodeNode: VideoDecodeNode | undefined;
	let audioContext: AudioContext | undefined;
	let audioDecodeNode: AudioDecodeNode | undefined;
	let currentCancel: (() => void) | null = null;

	// AudioDecodeNode extends GainNode, so volume is just its gain value. Runs
	// entirely client-side — no effect on the live MoQ stream. The effective
	// level is 0 when muted, otherwise the chosen volume.
	const gainValue: Accessor<number> = () => (muted() ? 0 : volume());

	createEffect(() => {
		const node = audioDecodeNode;
		if (!node) return;
		node.gain.value = gainValue();
	});

	// Unmuting when volume has been dragged to 0 would otherwise leave the
	// player silent with the unmuted icon — restore a default level.
	const toggleMute = () => {
		if (muted()) {
			setMuted(false);
			if (volume() <= 0) setVolume(1);
		} else {
			setMuted(true);
		}
	};

	const toggleFullscreen = () => {
		const el = previewEle;
		if (!el) return;
		if (document.fullscreenElement === el) {
			void document.exitFullscreen();
		} else {
			void el.requestFullscreen?.();
		}
	};

	const onFullscreenChange = () => setIsFullscreen(document.fullscreenElement === previewEle);

	onMount(() => {
		if (canvasEle) {
			videoContext = new VideoContext({ canvas: canvasEle });
			videoDecodeNode = new VideoDecodeNode(videoContext);
			videoDecodeNode.connect(videoContext.destination);

			// AudioContext for playback of the subscribed audio track.
			audioContext = new AudioContext();
			audioDecodeNode = new AudioDecodeNode(audioContext);
			audioDecodeNode.connect(audioContext.destination);
			// Apply the initial level (the gain effect below only fires on changes).
			audioDecodeNode.gain.value = gainValue();
		}
		document.addEventListener("fullscreenchange", onFullscreenChange);
	});

	onCleanup(() => {
		document.removeEventListener("fullscreenchange", onFullscreenChange);
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
			const subscribePath = props.path() as BroadcastPath; // snapshot at subscription start

			// Gate: the video loop awaits this before feeding any frames to the decoder.
			// Resolved the moment the first catalog group is received.
			let resolveFirstConfig!: () => void;
			const firstConfigReady = new Promise<void>((r) => {
				resolveFirstConfig = r;
			});
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
								// Decode Base64 initData → ArrayBuffer description (for avc1.* AVCC streams).
								let description: ArrayBuffer | undefined;
								if (videoTrack.initData) {
									const binary = atob(videoTrack.initData);
									const bytes = new Uint8Array(binary.length);
									for (let i = 0; i < binary.length; i++) {
										bytes[i] = binary.charCodeAt(i);
									}
									description = bytes.buffer;
								}
								const cfg: VideoDecoderConfig = {
									codec: videoTrack.codec,
									codedWidth: videoTrack.width,
									codedHeight: videoTrack.height,
									hardwareAcceleration: "prefer-software",
									optimizeForLatency: true,
									...(description ? { description } : {}),
								};
								videoDecodeNode!.configure(cfg);
								console.log("[Subscribe] VideoDecoder configured:", cfg.codec);
								// Update canvas bitmap dimensions to match the actual video so portrait
								// (or any non-default-aspect) streams are not stretched.
								if (videoTrack.width) setCanvasWidth(videoTrack.width);
								if (videoTrack.height) setCanvasHeight(videoTrack.height);
								console.log(
									"[Subscribe] catalog received, video codec:",
									videoTrack.codec,
								);
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
								console.log(
									"[Subscribe] audio decoder configured:",
									audioTrack.codec,
								);
							}
						}
					}
				},
			);

			session.subscribe(subscribePath, "video").then(
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
								console.log(
									"[Subscribe] video: catalog ready, starting decode loop",
								);

								let frameCount = 0;
								while (isSubscribed()) {
									const [group, groupErr] = await videoTrack.acceptGroup(
										ctx.done(),
									);
									if (groupErr) {
										if (!isSubscribed()) break;
										console.error(
											"[Subscribe] video acceptGroup error:",
											groupErr,
										);
										break;
									}

									let isKey = true;
									for await (const frame of group.frames()) {
										const { timestamp, data } = deserializeMediaFrame(
											frame.bytes,
										);

										// Log first 10 frames for diagnostics.
										if (frameCount < 10) {
											const hex = Array.from(data.slice(0, 48))
												.map((b: number) => b.toString(16).padStart(2, "0"))
												.join(" ");
											console.log(
												`[Subscribe] video #${frameCount}: type=${
													isKey ? "key" : "delta"
												} ts=${timestamp} len=${data.byteLength} hex=[${hex}]`,
											);
											frameCount++;
										}

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
									console.error("[Subscribe] video track error:", err);
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
									const [group, groupErr] = await audioMoqTrack.acceptGroup(
										ctx.done(),
									);
									if (groupErr) {
										if (!isSubscribed()) break;
										console.error(
											"moq: Error accepting audio group:",
											groupErr,
										);
										break;
									}
									for await (const frame of group.frames()) {
										const { timestamp, data } = deserializeMediaFrame(
											frame.bytes,
										);
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
		audioContext?.suspend().catch(() => {});
		setIsSubscribed(false);
	};

	return (
		<div class="subscribe-board">
			<h2>Subscribe Board</h2>

			<div class="controls">
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
					Subscribing to: {props.path()}
				</div>
			</Show>

			<div class="video-preview" ref={previewEle}>
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

			<div class="viewer-controls">
				<button
					type="button"
					class="copy-btn"
					onClick={toggleMute}
					aria-pressed={muted()}
					title={muted() ? "Unmute" : "Mute"}
				>
					{muted() ? "🔇" : "🔊"}
				</button>
				<input
					type="range"
					class="volume-slider"
					min={0}
					max={1}
					step={0.01}
					value={gainValue()}
					onInput={(e) => {
						const v = Number(e.currentTarget.value);
						setVolume(v);
						setMuted(v === 0);
					}}
					aria-label="Volume"
				/>
				<button
					type="button"
					class="copy-btn"
					onClick={toggleFullscreen}
					title={isFullscreen() ? "Exit fullscreen" : "Fullscreen"}
				>
					{isFullscreen() ? "⤢" : "⛶"}
				</button>
			</div>
		</div>
	);
}
