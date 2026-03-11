import { createEffect, createSignal, onMount, Show } from "solid-js";
import { type BroadcastPath, type GroupWriter, TrackMux } from "@okdaichi/moq";
import { Broadcast, type Track } from "@okdaichi/moq/msf";
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
import { useBroadcastPath } from "../useBroadcastPath.ts";

const GOP_DURATION = 1000; // 1 second

export function PublishBoard(props: { mux: TrackMux }) {
	const broadcastPath: BroadcastPath = useBroadcastPath();
	const mux = props.mux;

	const [sourceType, setSourceType] = createSignal<MediaSourceType>("camera");
	const [isStreaming, setIsStreaming] = createSignal(false);
	const [error, setError] = createSignal<string | null>(null);
	const [canvasWidth, setCanvasWidth] = createSignal(1280);
	const [canvasHeight, setCanvasHeight] = createSignal(720);
	// Resolved asynchronously by the first effect below; read synchronously by the second effect and startStreaming.
	const [encoderConfig, setEncoderConfig] = createSignal<VideoEncoderConfig | undefined>();

	let canvasEle: HTMLCanvasElement | undefined;
	let lastKeyframeTime = 0;
	let videoContext: VideoContext | undefined;
	let sourceNode: MediaStreamVideoSourceNode | null = null;
	let videoEncodeNode: VideoEncodeNode | undefined;
	let audioContext: AudioContext | undefined;
	let audioEncodeNode: AudioEncodeNode | undefined;
	// Active Broadcast instance — set when streaming starts; cleared on stop.
	let broadcastRef: Broadcast | undefined;
	// Audio track catalog entry — set in startStreaming if audio is available.
	let audioTrackDef: Track | undefined;

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

	// Effect 1: reads signals synchronously, then fire-and-forgets the async fetch.
	// All signal reads happen before the first await → tracking is complete and reliable.
	createEffect(() => {
		const width = canvasWidth();
		const height = canvasHeight();
		if (!videoEncodeNode || width <= 0 || height <= 0) return;
		void videoEncoderConfig({ width, height, bitrate: 2_500_000, frameRate: 30, tryHardware: true })
			.then(setEncoderConfig);
	});

	// Effect 2: purely synchronous — applies the resolved config to the encoder and catalog.
	createEffect(() => {
		const config = encoderConfig();
		if (!config || !videoEncodeNode) return;
		// Use avc3 (inline SPS/PPS) — no separate description field required.
		const inlineCodec = config.codec.replace(/^avc1\./, "avc3.");
		videoEncodeNode.configure(config);
		if (broadcastRef) {
			const updatedTrack: Track = {
				name: "video",
				role: "video",
				packaging: "loc",
				isLive: true,
				codec: inlineCodec,
				width: config.width,
				height: config.height,
			};
			const tracks: Track[] = audioTrackDef ? [updatedTrack, audioTrackDef] : [updatedTrack];
			broadcastRef.setCatalog({ version: 1, tracks }).catch(console.error);
		}
	});

	let publishCtx: Context;
	let cancelPublish: CancelFunc;

	const startStreaming = async () => {
		[publishCtx, cancelPublish] = withCancel(background());

		if (!videoContext || !videoEncodeNode) {
			setError("Video context not initialized");
			return;
		}

		// Acquire media first — we need the actual track dimensions before configuring the encoder.
		let stream: MediaStream;
		try {
			setError(null);
			stream = await getMediaStream(sourceType());
		} catch (err) {
			const errorMessage = err instanceof Error ? err.message : String(err);
			setError(errorMessage);
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
				const bitmap = await (imageCapture as unknown as { grabFrame(): Promise<ImageBitmap> }).grabFrame();
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

		// Use pre-computed config if dimensions match; otherwise recompute at actual size.
		let config = encoderConfig();
		if (!config || config.width !== actualWidth || config.height !== actualHeight) {
			config = await videoEncoderConfig({
				width: actualWidth,
				height: actualHeight,
				bitrate: 2_500_000,
				frameRate: 30,
				tryHardware: true,
			});
			videoEncodeNode.configure(config);
			// Update signals so Effect 2 and the canvas preview stay consistent.
			setEncoderConfig(config);
			setCanvasWidth(actualWidth);
			setCanvasHeight(actualHeight);
		}
		const inlineCodec = config.codec.replace(/^avc1\./, "avc3.");

		// Set up audio encoder (AudioContext.resume() works here as we're in a user-gesture handler).
		if (audioContext && audioEncodeNode) {
			try {
				await audioContext.resume();
				const audioCfg = await audioEncoderConfig({
					sampleRate: audioContext.sampleRate,
					channels: audioContext.destination.channelCount,
				});
				audioEncodeNode.configure(audioCfg);
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
				console.warn("[Publish] audio setup failed, continuing without audio:", err);
			}
		}

		const initialTrack: Track = {
			name: "video",
			role: "video",
			packaging: "loc",
			isLive: true,
			codec: inlineCodec,
			width: config.width,
			height: config.height,
		};

		// Broadcast auto-serves the "catalog" track as MSF catalog JSON.
		const initialTracks: Track[] = audioTrackDef ? [initialTrack, audioTrackDef] : [initialTrack];
		const broadcast = new Broadcast({ version: 1, tracks: initialTracks });
		broadcastRef = broadcast;

		// Register video track handler — runs the encoder loop when subscribed.
		await broadcast.registerTrack(initialTrack, {
			async serveTrack(trackWriter) {
				if (!videoEncodeNode) throw new Error("Encode node not initialized");

				let currentGroup: GroupWriter | undefined;

				const { done } = videoEncodeNode.encodeTo({
					output: async (chunk: EncodedVideoChunk, _?: VideoDecoderConfig) => {
						if (chunk.type === "key") {
							if (currentGroup) void currentGroup.close();
							const [group, err] = await trackWriter.openGroup();
							if (err) return err;
							currentGroup = group;
						} else if (!currentGroup) {
							return; // drop delta frames until first keyframe
						}

						const err = await currentGroup.writeFrame(new MediaFrame(chunk));
						if (err) throw err;
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
			publishCtx.done(),
			broadcastPath,
			broadcast,
		);

		// Connect source nodes and start encoding.
		sourceNode = new MediaStreamVideoSourceNode(videoContext, { mediaStream: stream });
		sourceNode.connect(videoContext.destination);
		sourceNode.connect(videoEncodeNode);
		sourceNode.start();

		// Route audio from the media stream into AudioEncodeNode.
		if (audioContext && audioEncodeNode) {
			try {
				const audioSource = audioContext.createMediaStreamSource(stream);
				audioSource.connect(audioEncodeNode);
			} catch (err) {
				console.warn("[Publish] failed to connect audio source:", err);
			}
		}

		setIsStreaming(true);
		console.log(`Started streaming from ${sourceType()}`);
	};

	const stopStreaming = () => {
		cancelPublish();
		broadcastRef = undefined;
		audioTrackDef = undefined;
		audioContext?.suspend().catch(() => {});
		if (sourceNode) {
			sourceNode.stop();
			sourceNode.dispose();
			sourceNode = null;
		}
		setIsStreaming(false);
	};

	return (
		<div class="publish-board">
			<h2>Publish Board</h2>

			<div class="controls">
				<div class="source-selector">
					<label for="source-type">Media Source:</label>
					<select
						id="source-type"
						value={sourceType()}
						onChange={(e) => setSourceType(e.currentTarget.value as MediaSourceType)}
						disabled={isStreaming()}
					>
						<option value="camera">Camera</option>
						<option value="screen">Screen Share</option>
					</select>
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
				<div class="error-message">
					Error: {error()}
				</div>
			</Show>

			<Show when={isStreaming()}>
				<div class="status-message">
					Streaming from: {sourceType()}
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
