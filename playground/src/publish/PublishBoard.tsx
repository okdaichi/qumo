import { type Accessor, createSignal, onCleanup, onMount, Show } from "solid-js";
import { type BroadcastPath, type GroupWriter, TrackMux } from "@qumo/moq";
import { Broadcast, type Track } from "@qumo/moq/msf";
import {
	AudioEncodeNode,
	audioEncoderConfig,
	MediaStreamVideoSourceNode,
	VideoContext,
	VideoEncodeNode,
	videoEncoderConfig,
} from "@okdaichi/av-nodes";
import { getMediaStream, type MediaSourceType } from "./media.ts";
import { background, type CancelFunc, type Context, withCancel } from "@okdaichi/golikejs/context";
import { MediaFrame } from "./media_frame.ts";
import { friendlyMessage } from "../errors.ts";
import { createStatsTicker } from "../stats.ts";
import { Camera, Monitor } from "lucide-solid";
import type { Component } from "solid-js";

// Encode an ArrayBuffer or ArrayBufferView as a Base64 string.
function encodeBase64(buf: ArrayBufferLike | ArrayBufferView): string {
	const bytes = ArrayBuffer.isView(buf)
		? new Uint8Array(buf.buffer as ArrayBuffer, buf.byteOffset, buf.byteLength)
		: new Uint8Array(buf as ArrayBuffer);
	let binary = "";
	for (const b of bytes) binary += String.fromCharCode(b);
	return btoa(binary);
}

const GOP_DURATION = 1000; // 1 second

// Encode-quality presets (#135). Resolution maps to getUserMedia `ideal`
// constraints (the camera picks the nearest mode); the encoder then encodes at
// the actual captured dimensions. Bitrate/framerate go straight to the encoder.
const RESOLUTIONS: Record<string, { width: number; height: number }> = {
	"480p": { width: 854, height: 480 },
	"720p": { width: 1280, height: 720 },
	"1080p": { width: 1920, height: 1080 },
};
const FRAMERATES = [24, 30, 60] as const;
const BITRATE_MIN = 500_000;
const BITRATE_MAX = 6_000_000;
const BITRATE_STEP = 100_000;

// Media-source options for the segmented switcher. `label` is also used for the
// "Streaming from" status line so it doesn't show the raw signal value. Icons
// come from lucide-solid (public icon set) rather than hand-rolled SVG.
const SOURCES: { id: MediaSourceType; label: string; icon: Component<{ class?: string }> }[] = [
	{ id: "camera", label: "Camera", icon: Camera },
	{ id: "screen", label: "Screen", icon: Monitor },
];

export function PublishBoard(props: { mux: TrackMux; path: Accessor<string> }) {
	const mux = props.mux;

	const [sourceType, setSourceType] = createSignal<MediaSourceType>("camera");
	const [isStreaming, setIsStreaming] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Encode-quality controls (applied at Start; stop+restart to change mid-session).
	const [resolution, setResolution] = createSignal<keyof typeof RESOLUTIONS>("720p");
	const [framerate, setFramerate] = createSignal<(typeof FRAMERATES)[number]>(30);
	const [bitrate, setBitrate] = createSignal(2_500_000);

	// Live stats overlay (#139): fps + media bitrate from a 1s rolling meter, plus
	// the encoder's queue depth sampled on the same tick. Cleared on stop.
	const [encQueue, setEncQueue] = createSignal(0);
	const videoStats = createStatsTicker(
		1000,
		() => setEncQueue(videoEncodeNode?.encodeQueueSize ?? 0),
	);
	onCleanup(() => videoStats.stop());

	let canvasEle: HTMLCanvasElement | undefined;
	let lastKeyframeTime = 0;
	let videoContext: VideoContext | undefined;
	let sourceNode: MediaStreamVideoSourceNode | null = null;
	let videoEncodeNode: VideoEncodeNode | undefined;
	let audioContext: AudioContext | undefined;
	let audioEncodeNode: AudioEncodeNode | undefined;
	// Audio track catalog entry — set in startStreaming if audio is available.
	let audioTrackDef: Track | undefined;
	// Whether the audio encoder is currently in the "configured" state. The
	// audio source must NEVER be routed into an unconfigured AudioEncodeNode:
	// its internal worklet→encoder loop would call encode() on an unconfigured
	// codec every frame, logging InvalidStateError in an infinite loop. This
	// is set only after configure() succeeds and cleared on stop/unmount.
	let audioConfigured = false;

	onMount(() => {
		if (canvasEle) {
			videoContext = new VideoContext({ canvas: canvasEle });
			videoEncodeNode = new VideoEncodeNode(videoContext, {
				isKey: (timestamp, _) => {
					// timestamp is in microseconds, so convert GOP_DURATION to microseconds
					if (timestamp - lastKeyframeTime >= GOP_DURATION * 1000) {
						lastKeyframeTime = timestamp;
						return true;
					}
					return false;
				},
			});

			// Set canvas dimensions based on the actual canvas size.
			setCanvasWidth(videoContext.destination.canvas.width);
			setCanvasHeight(videoContext.destination.canvas.height);

			audioContext = new AudioContext({ sampleRate: 48000 });
			audioEncodeNode = new AudioEncodeNode(audioContext);
		}
	});

	let publishCtx: Context | undefined;
	let cancelPublish: CancelFunc | undefined;

	const startStreaming = async () => {
		[publishCtx, cancelPublish] = withCancel(background());

		if (!videoContext || !videoEncodeNode) {
			setError("Video context not initialized");
			return;
		}

		// Acquire media first — we need the actual track dimensions before configuring the encoder.
		// Apply the chosen resolution/framerate as getUserMedia ideal constraints.
		const target = RESOLUTIONS[resolution()];
		let stream: MediaStream;
		try {
			setError(null);
			stream = await getMediaStream(sourceType(), {
				width: target.width,
				height: target.height,
				frameRate: framerate(),
			});
		} catch (err) {
			// Pass the source type so a denied screen-share reads as screen-share,
			// not "Camera or microphone".
			setError(friendlyMessage(err, sourceType()));
			console.error("Failed to start streaming:", err);
			return;
		}

		// Read actual video dimensions from the first real frame.
		// getSettings() can lie about dimensions (e.g. webcam rotation metadata).
		// ImageCapture.grabFrame() returns an ImageBitmap whose .width/.height
		// always reflect the actual pixel buffer the browser will give us through
		// MediaStreamTrackProcessor — so this is the ground truth.
		const videoTrack = stream.getVideoTracks()[0];
		let actualWidth: number;
		let actualHeight: number;
		if (videoTrack && "ImageCapture" in globalThis) {
			try {
				const imageCapture = new ImageCapture(videoTrack);
				const bitmap =
					await (imageCapture as unknown as { grabFrame(): Promise<ImageBitmap> })
						.grabFrame();
				actualWidth = bitmap.width;
				actualHeight = bitmap.height;
				bitmap.close();
			} catch (err) {
				console.warn("[Publish] grabFrame failed, falling back to getSettings():", err);
				const s = videoTrack.getSettings();
				actualWidth = s.width ?? canvasWidth();
				actualHeight = s.height ?? canvasHeight();
			}
		} else {
			const s = videoTrack?.getSettings();
			actualWidth = s?.width ?? canvasWidth();
			actualHeight = s?.height ?? canvasHeight();
		}

		// Always (re)compute the encoder config at the actual captured dimensions
		// with the current quality picks. videoEncoderConfig returns
		// WebCodecs-normalized values (bitrate scaled per codec), so don't cache
		// or compare — just apply the live values here at Start.
		const br = bitrate();
		const fps = framerate();
		let config: Awaited<ReturnType<typeof videoEncoderConfig>>;
		try {
			config = await videoEncoderConfig({
				width: actualWidth,
				height: actualHeight,
				bitrate: br,
				frameRate: fps,
				tryHardware: true,
			});
			videoEncodeNode.configure(config);
		} catch (err) {
			// Codec/encoder unsupported — release the camera we just acquired.
			stream.getTracks().forEach((t) => t.stop());
			setError(friendlyMessage(err));
			console.error("Failed to configure video encoder:", err);
			return;
		}
		// Size the preview canvas to the actual stream dimensions.
		setCanvasWidth(actualWidth);
		setCanvasHeight(actualHeight);

		// Set up audio encoder (AudioContext.resume() works here as we're in a user-gesture handler).
		if (audioContext && audioEncodeNode) {
			try {
				await audioContext.resume();
				const audioCfg = await audioEncoderConfig({
					sampleRate: audioContext.sampleRate,
					channels: audioContext.destination.channelCount,
				});
				audioEncodeNode.configure(audioCfg);
				audioConfigured = true;
				audioTrackDef = {
					name: "audio",
					role: "audio",
					packaging: "loc",
					isLive: true,
					codec: audioCfg.codec,
					samplerate: audioCfg.sampleRate,
					channelConfig: String(audioCfg.numberOfChannels),
				};
			} catch (err) {
				// configure() never ran (or context resume failed): keep
				// audioConfigured false so we don't connect a MediaStreamSource
				// into an unconfigured encoder below.
				audioConfigured = false;
				console.warn("[Publish] audio setup failed, continuing without audio:", err);
			}
		}

		const initialTrack: Track = {
			name: "video",
			role: "video",
			packaging: "loc",
			isLive: true,
			codec: config.codec,
			width: config.width,
			height: config.height,
		};

		// Broadcast auto-serves the "catalog" track as MSF catalog JSON.
		const initialTracks: Track[] = audioTrackDef
			? [initialTrack, audioTrackDef]
			: [initialTrack];
		const broadcast = new Broadcast({ version: 1, tracks: initialTracks });

		// Register video track handler — runs the encoder loop when subscribed.
		await broadcast.registerTrack(initialTrack, {
			async serveTrack(trackWriter) {
				if (!videoEncodeNode) throw new Error("Encode node not initialized");

				let currentGroup: GroupWriter | undefined;
				// Tracks whether we have published an initData-bearing catalog for the
				// current encoder configuration. Reset when the encoder is reconfigured.
				let initDataPublished = false;

				const { done } = videoEncodeNode.encodeTo({
					output: async (
						chunk: EncodedVideoChunk,
						decoderConfig?: VideoDecoderConfig,
					) => {
						// When the encoder emits a new decoder config (first keyframe or
						// parameter change), push the SPS/PPS description into the catalog
						// as a Base64-encoded initData field so subscribers can configure
						// their VideoDecoder with the correct description.
						if (decoderConfig?.description && !initDataPublished) {
							initDataPublished = true;
							const initData = encodeBase64(
								decoderConfig.description as ArrayBufferLike,
							);
							const updatedTrack: Track = { ...initialTrack, initData };
							const tracks: Track[] = audioTrackDef
								? [updatedTrack, audioTrackDef]
								: [updatedTrack];
							broadcast.setCatalog({ version: 1, tracks }).catch(console.error);
						}

						if (chunk.type === "key") {
							if (currentGroup) void currentGroup.close();
							const [group, err] = await trackWriter.openGroup();
							if (err) return err;
							currentGroup = group;
						} else if (!currentGroup) {
							return; // drop delta frames until first keyframe
						}

						const err = await currentGroup!.writeFrame(new MediaFrame(chunk));
						if (err) throw err;
						// Tally the published frame for the live stats overlay.
						videoStats.mark(chunk.byteLength);
					},
				});

				await done;
			},
		});

		// Register audio track handler if audio is available.
		if (audioTrackDef && audioEncodeNode) {
			const capturedAudioEncodeNode = audioEncodeNode;
			await broadcast.registerTrack(audioTrackDef, {
				async serveTrack(trackWriter) {
					const { done } = capturedAudioEncodeNode.encodeTo({
						// Each Opus frame is independently decodable — one group per frame
						// lets subscribers join at any point without waiting for a keyframe.
						async output(chunk: EncodedAudioChunk, _?: AudioDecoderConfig) {
							const [group, err] = await trackWriter.openGroup();
							if (err) return err;
							const writeErr = await group.writeFrame(new MediaFrame(chunk));
							void group.close();
							if (writeErr) throw writeErr;
						},
					});
					await done;
				},
			});
		}

		// Announce to relay — Broadcast routes "catalog" and "video" internally.
		mux.publish(
			publishCtx!.done(),
			props.path() as BroadcastPath,
			broadcast,
		);

		// Connect source nodes and start encoding.
		sourceNode = new MediaStreamVideoSourceNode(videoContext, { mediaStream: stream });
		sourceNode.connect(videoContext.destination);
		sourceNode.connect(videoEncodeNode);
		sourceNode.start();

		// Route audio from the media stream into AudioEncodeNode. Only do this
		// when the encoder was actually configured above — feeding an
		// unconfigured encoder makes its worklet loop throw forever.
		if (audioConfigured && audioContext && audioEncodeNode) {
			try {
				const audioSource = audioContext.createMediaStreamSource(stream);
				audioSource.connect(audioEncodeNode);
			} catch (err) {
				console.warn("[Publish] failed to connect audio source:", err);
			}
		}

		setIsStreaming(true);
		videoStats.start();
		console.log(`Started streaming from ${sourceType()}`);
	};

	const stopStreaming = () => {
		// cancelPublish is only assigned inside startStreaming, but teardown()
		// calls this on unmount too — which can run before Start was ever clicked.
		cancelPublish?.();
		audioTrackDef = undefined;
		audioConfigured = false;
		audioContext?.suspend().catch(() => {});
		if (sourceNode) {
			sourceNode.stop();
			sourceNode.dispose();
			sourceNode = null;
		}
		videoStats.stop();
		setIsStreaming(false);
	};

	// Full teardown on unmount. Scenario switches remount <ScenarioView>, so
	// without this every echo visit would leak an AudioContext, a VideoContext,
	// and both encode nodes (their internal worklet→encoder loops keep running).
	// After a few switches the leaked AudioContexts push the browser into a
	// degraded state where audio setup fails — which is how we end up routing
	// audio into an unconfigured encoder. Disposing here stops that at the source.
	const teardown = () => {
		stopStreaming();
		// Fire-and-forget: dispose/close are async but we're unmounting.
		videoEncodeNode?.dispose().catch(() => {});
		videoEncodeNode = undefined;
		audioEncodeNode?.dispose().catch(() => {});
		audioEncodeNode = undefined;
		videoContext?.close().catch(() => {});
		videoContext = undefined;
		audioContext?.close().catch(() => {});
		audioContext = undefined;
	};
	onCleanup(() => teardown());

	return (
		<div class="publish-board">
			<h2>Publish Board</h2>

			<div class="controls">
				<div class="source-selector">
					<label>Media Source</label>
					<div class="segmented" role="group" aria-label="Media source">
						{SOURCES.map((s) => {
							const Icon = s.icon;
							return (
								<button
									type="button"
									class="segmented-btn"
									classList={{ active: sourceType() === s.id }}
									aria-pressed={sourceType() === s.id}
									disabled={isStreaming()}
									onClick={() => setSourceType(s.id)}
									title={isStreaming()
										? "Stop streaming to switch source"
										: s.label}
								>
									<Icon class="source-icon" /> {s.label}
								</button>
							);
						})}
					</div>
				</div>

				<div class="encoder-controls">
					<label>
						Quality
						<select
							value={resolution()}
							onChange={(e) =>
								setResolution(e.currentTarget.value as keyof typeof RESOLUTIONS)}
							disabled={isStreaming()}
						>
							{Object.entries(RESOLUTIONS).map(([id]) => (
								<option value={id}>{id}</option>
							))}
						</select>
					</label>
					<label>
						FPS
						<select
							value={framerate()}
							onChange={(e) => setFramerate(
								Number(e.currentTarget.value) as (typeof FRAMERATES)[number],
							)}
							disabled={isStreaming()}
						>
							{FRAMERATES.map((fps) => <option value={fps}>{fps}</option>)}
						</select>
					</label>
					<label class="bitrate-control">
						Bitrate
						<input
							type="range"
							min={BITRATE_MIN}
							max={BITRATE_MAX}
							step={BITRATE_STEP}
							value={bitrate()}
							onInput={(e) => setBitrate(Number(e.currentTarget.value))}
							disabled={isStreaming()}
						/>
						<span class="bitrate-value">{(bitrate() / 1_000_000).toFixed(1)} Mbps</span>
					</label>
				</div>

				<div class="stream-controls">
					<Show
						when={!isStreaming()}
						fallback={
							<button type="button" onClick={stopStreaming} class="btn-stop">
								Stop Streaming
							</button>
						}
					>
						<button type="button" onClick={startStreaming} class="btn-start">
							Start Streaming
						</button>
					</Show>
				</div>
			</div>

			<Show when={error()}>
				<div class="error-message">{error()}</div>
			</Show>

			<Show when={isStreaming()}>
				<div class="status-message">
					Streaming from:{" "}
					{SOURCES.find((s) => s.id === sourceType())?.label ?? sourceType()}
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
				<Show when={isStreaming()}>
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
						<Show when={encQueue() > 0}>
							<div>
								<dt>queue</dt>
								<dd>{encQueue()}</dd>
							</div>
						</Show>
					</dl>
				</Show>
			</div>
		</div>
	);
}
