import { type Accessor, createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { AudioDecodeNode, VideoContext, VideoDecodeNode } from "@okdaichi/av-nodes";
import { type Session, SubscribeErrorCode } from "@qumo/moq";
import { parseCatalog } from "@qumo/moq/msf";
import { deserializeMediaFrame } from "../publish/media_frame.ts";
import { background, withCancel } from "@okdaichi/golikejs/context";
import { friendlyMessage } from "../errors.ts";
import { createLogger, createMediaLogger, MediaTags } from "@okdaichi/media-log";
import { createStatsTicker } from "../stats.ts";
import type { BroadcastPath } from "@qumo/moq";

// Tagged, structured logging via @okdaichi/media-log. The video (decoder) logger
// also carries media meters (fps/bitrate/gauge) that flush one diagnostic line
// per second alongside the UI overlay.
const log = createLogger("subscribe");
const videoLog = createMediaLogger(MediaTags.decoder);
const audioLog = createLogger(MediaTags.audio);
const decFps = videoLog.meter.fps("decode");
const decBitrate = videoLog.meter.bitrate("ingress");
const rttGauge = videoLog.meter.gauge("rtt", { unit: "ms" });
const queueGauge = videoLog.meter.gauge("decode queue");

export function SubscribeBoard(props: { session: Promise<Session>; path: Accessor<string> }) {
	const [isSubscribed, setIsSubscribed] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Viewer controls (#136): pure client-side, no timeline (MoQ is live-only).
	const [volume, setVolume] = createSignal(1);
	const [muted, setMuted] = createSignal(false);
	const [isFullscreen, setIsFullscreen] = createSignal(false);

	// Live stats overlay (#139): fps + media bitrate from a 1s rolling meter,
	// plus decoder queue depth and the session RTT sampled on the same tick.
	const [decQueue, setDecQueue] = createSignal(0);
	const [rtt, setRtt] = createSignal(0);
	let statsSession: Session | undefined;
	const videoStats = createStatsTicker(1000, () => {
		const q = videoDecodeNode?.decodeQueueSize ?? 0;
		setDecQueue(q);
		queueGauge.sample(q);
		if (statsSession) {
			void statsSession.getStats().then((s) => {
				const r = s.rtt ?? 0;
				setRtt(r);
				rttGauge.sample(r);
			});
		}
	});

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
		// Read gainValue() FIRST so the effect subscribes to muted/volume even
		// when audioDecodeNode isn't set yet on the first run — otherwise the
		// early return would track nothing and the effect would never re-run,
		// leaving mute/volume non-functional.
		const g = gainValue();
		const node = audioDecodeNode;
		if (node) node.gain.value = g;
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
			// Match the source rate (AAC 48 kHz). av-nodes' worklet converts
			// frame PTS -> sample offset using context.sampleRate; if the context
			// runs at the system default (e.g. 44100) every 1024-sample block is
			// scheduled at the wrong offset under timestamp scheduling.
			// PublishBoard already pins 48000.
			audioContext = new AudioContext({ sampleRate: 48000 });
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
			statsSession = session; // for RTT sampling in the stats ticker
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
						// Catalog TrackNotFound is the common "nobody is publishing to
						// this path yet" case — friendlyMessage maps it to actionable text.
						setError(friendlyMessage(catalogErr));
						log.warn("catalog subscribe failed", { err: catalogErr });
						// No catalog => no stream to recover; drop back to Start so the
						// user can retry without clicking Stop first.
						setIsSubscribed(false);
						return;
					}

					while (isSubscribed()) {
						const [group, groupErr] = await catalogTrack.acceptGroup(ctx.done());
						if (groupErr) {
							if (!isSubscribed()) break;
							log.warn("catalog acceptGroup failed", { err: groupErr });
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
								log.info("video decoder configured", { codec: cfg.codec });
								// Update canvas bitmap dimensions to match the actual video so portrait
								// (or any non-default-aspect) streams are not stretched.
								if (videoTrack.width) setCanvasWidth(videoTrack.width);
								if (videoTrack.height) setCanvasHeight(videoTrack.height);
								log.info("catalog received", { videoCodec: videoTrack.codec });
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
								audioLog.info("audio decoder configured", {
									codec: audioTrack.codec,
								});
							}
						}
					}
				},
			);

			session.subscribe(subscribePath, "video").then(
				([videoTrack, videoErr]) => {
					if (videoErr) {
						if (!isSubscribed()) return;
						setError(friendlyMessage(videoErr));
						videoLog.warn("subscribe failed", { err: videoErr });
						setIsSubscribed(false);
						return;
					}

					const videoStream = new ReadableStream<EncodedVideoChunk>({
						async start(controller) {
							try {
								// Wait for the first catalog before feeding any frames.
								await firstConfigReady;
								videoLog.info("catalog ready, starting decode loop");

								while (isSubscribed()) {
									const [group, groupErr] = await videoTrack.acceptGroup(
										ctx.done(),
									);
									if (groupErr) {
										if (!isSubscribed()) break;
										videoLog.error("acceptGroup failed", { err: groupErr });
										break;
									}

									let isKey = true;
									for await (const frame of group.frames()) {
										const { timestamp, data } = deserializeMediaFrame(
											frame.bytes,
										);

										const chunk = new EncodedVideoChunk({
											type: isKey ? "key" : "delta",
											timestamp,
											data,
										});
										controller.enqueue(chunk);
										// Tally the decoded frame for the live stats overlay…
										videoStats.mark(data.byteLength);
										// …and for the periodic diagnostic log (fps + bitrate).
										decFps.mark();
										decBitrate.mark(data.byteLength);
										isKey = false;
									}
								}
								controller.close();
							} catch (err) {
								if (isSubscribed()) {
									videoLog.error("video track error", { err });
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
						audioLog.warn("subscribe failed", { err: audioMoqErr });
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
										audioLog.error("acceptGroup failed", { err: groupErr });
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
									audioLog.error("audio track error", { err });
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
			videoStats.start();
		} catch (err) {
			setError(friendlyMessage(err));
			log.error("failed to start subscribing", { err });
			setIsSubscribed(false);
		}
	};

	const stopSubscribing = () => {
		if (currentCancel) {
			currentCancel();
			currentCancel = null;
		}
		audioContext?.suspend().catch(() => {});
		statsSession = undefined;
		videoStats.stop();
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
				<div class="error-message">{error()}</div>
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
				<Show when={isSubscribed()}>
					<dl class="stats-overlay" aria-live="off">
						<div>
							<dt>res</dt>
							<dd>{canvasWidth()}×{canvasHeight()}</dd>
						</div>
						<div>
							<dt>fps</dt>
							<dd>{videoStats.stats().fps}</dd>
						</div>
						<div>
							<dt>br</dt>
							<dd>{videoStats.stats().bitrateMbps} Mbps</dd>
						</div>
						<Show when={rtt() > 0}>
							<div>
								<dt>rtt</dt>
								<dd>{rtt()} ms</dd>
							</div>
						</Show>
						<Show when={decQueue() > 0}>
							<div>
								<dt>queue</dt>
								<dd>{decQueue()}</dd>
							</div>
						</Show>
					</dl>
				</Show>
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
