import { createEffect, createSignal, onMount, Show } from "solid-js";
import { type BroadcastPath, GroupWriter, TrackMux } from "@okdaichi/moq";
import {
	MediaStreamVideoSourceNode,
	VideoContext,
	VideoEncodeNode,
	videoEncoderConfig,
} from "@okdaichi/av-nodes";
import { getMediaStream, type MediaSourceType } from "./media.ts";
import { background, type CancelFunc, type Context, withCancel } from "@okdaichi/golikejs/context";
import { MediaFrame } from "./media_frame.ts";
import type { VideoMetadata } from "../metadata/mod.ts";
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
	// Signal holding the latest VideoMetadata from the encoder — drives video.meta MoQ track reactively.
	const [currentVideoMeta, setCurrentVideoMeta] = createSignal<VideoMetadata | undefined>();

	let canvasEle: HTMLCanvasElement | undefined;
	let lastKeyframeTime = 0;
	let videoContext: VideoContext | undefined;
	let sourceNode: MediaStreamVideoSourceNode | null = null;
	let videoEncodeNode: VideoEncodeNode | undefined;
	// Writer into the video.meta TransformStream — set when streaming starts.
	let videoMetaWriterRef: WritableStreamDefaultWriter<VideoMetadata> | undefined;

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

			// VideoContextのcanvasサイズから初期値を取得
			setCanvasWidth(videoContext.destination.canvas.width);
			setCanvasHeight(videoContext.destination.canvas.height);
		}
	});

	// Canvasサイズが変更されたら自動で再configure
	createEffect(async () => {
		const width = canvasWidth();
		const height = canvasHeight();

		if (videoEncodeNode && width > 0 && height > 0) {
			const videoConfig = await videoEncoderConfig({
				width,
				height,
				bitrate: 2_500_000,
				frameRate: 30,
				tryHardware: true,
			});
			videoEncodeNode.configure(videoConfig);
			console.log(`Video encoder reconfigured to ${width}x${height}`);
		}
	});

	// MoQ reactive: whenever the encoder emits updated codec params (e.g. after a resolution
	// change), write them into the video.meta TransformStream.  The track handler picks them
	// up and publishes a new MoQ group — subscribers looping on acceptGroup() receive it.
	createEffect(() => {
		const meta = currentVideoMeta();
		if (meta && videoMetaWriterRef) {
			videoMetaWriterRef.write(meta).catch(console.error);
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

		// Create metadata streams BEFORE announcing or acquiring media.
		const videoMetaStream = new TransformStream<VideoMetadata>();
		videoMetaWriterRef = videoMetaStream.writable.getWriter();

		// Announce tracks to the relay FIRST — before starting media capture.
		// This ensures the relay has a handler registered before any subscriber
		// attempts to SUBSCRIBE, preventing RESET_STREAM rejections.
		mux.publishFunc(
			publishCtx.done(),
			broadcastPath,
			async (track) => {
				switch (track.trackName) {
					case "video": {
						if (!videoEncodeNode) {
							throw new Error("Encode node not initialized");
						}

						let currentGroup: GroupWriter | undefined = undefined;

						const { done } = videoEncodeNode.encodeTo({
							output: async (
								chunk: EncodedVideoChunk,
								decoderConfig?: VideoDecoderConfig,
							) => {
								if (chunk.type === "key") {
									if (currentGroup) void currentGroup.close();
									const [group, err] = await track.openGroup();
									if (err) return err;
									currentGroup = group;
									if (decoderConfig) {
										// Signal update triggers createEffect → videoMetaWriterRef.write() → MoQ group publish.
										setCurrentVideoMeta({ ...decoderConfig, startGroup: currentGroup.sequence });
									}
								} else if (!currentGroup) {
									// Drop delta frames until we get a keyframe.
									return;
								}

								const err = await currentGroup.writeFrame(new MediaFrame(chunk));
								if (err) throw err;
							},
						});

						await done;
						break;
					}

					case "video.meta": {
					const reader = videoMetaStream.readable.getReader();
					try {
						while (true) {
							const { value: meta, done } = await reader.read();
							if (done) break;
							const buf = new TextEncoder().encode(JSON.stringify(meta));
							const [group, openErr] = await track.openGroup();
							if (openErr) {
								console.error("video.meta: openGroup error", openErr);
								break;
							}
							const writeErr = await group.writeFrame(new MediaFrame({
								timestamp: Date.now() * 1000,
								byteLength: buf.byteLength,
								copyTo(target: ArrayBuffer | ArrayBufferView) {
									new Uint8Array(target as ArrayBuffer).set(buf);
								},
							}));
							if (writeErr) console.error("video.meta: writeFrame error", writeErr);
							void group.close();
						}
					} finally {
						reader.releaseLock();
					}
					break;
				}

					default:
						return;
				}
			},
		);

		// Acquire media and start encoding.
		try {
			setError(null);
			const stream = await getMediaStream(sourceType());

			sourceNode = new MediaStreamVideoSourceNode(videoContext, { mediaStream: stream });
			sourceNode.connect(videoContext.destination);
			sourceNode.connect(videoEncodeNode);
			sourceNode.start();

			setIsStreaming(true);
			console.log(`Started streaming from ${sourceType()}`);
		} catch (err) {
			const errorMessage = err instanceof Error ? err.message : String(err);
			setError(errorMessage);
			console.error("Failed to start streaming:", err);
			return;
		}

	};

	const stopStreaming = () => {
		cancelPublish();
		videoMetaWriterRef = undefined;
		if (sourceNode) {
			sourceNode.stop();
			sourceNode.dispose();
			sourceNode = null;
		}
		setIsStreaming(false);
		console.log("Stopped streaming");
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
