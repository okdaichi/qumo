import { createEffect, createSignal, onMount, Show } from "solid-js";
import { type BroadcastPath, GroupWriter, TrackMux } from "@okdaichi/moq";
import {
	AudioEncodeNode,
	MediaStreamVideoSourceNode,
	VideoContext,
	VideoEncodeNode,
	videoEncoderConfig,
} from "@okdaichi/av-nodes";
import { getMediaStream, type MediaSourceType } from "./media.ts";
import { background, type CancelFunc, type Context, withCancel } from "@okdaichi/golikejs/context";
import { MediaFrame } from "./media_frame.ts";
import type { AudioMetadata, VideoMetadata } from "../metadata/mod.ts";
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

	let canvasEle: HTMLCanvasElement | undefined;
	let lastKeyframeTime = 0;
	let videoContext: VideoContext | undefined;
	let sourceNode: MediaStreamVideoSourceNode | null = null;
	let videoEncodeNode: VideoEncodeNode | undefined;
	let audioEncodeNode: AudioEncodeNode | undefined;

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

	let publishCtx: Context;
	let cancelPublish: CancelFunc;

	const startStreaming = async () => {
		console.log("[publish] startStreaming invoked");
		[publishCtx, cancelPublish] = withCancel(background());

		try {
			setError(null);

			if (!videoContext || !videoEncodeNode) {
				throw new Error("Video context not initialized");
			}

			const stream = await getMediaStream(sourceType());

			// Create and configure source node
			sourceNode = new MediaStreamVideoSourceNode(videoContext, { mediaStream: stream });
			sourceNode.connect(videoContext.destination);
			sourceNode.connect(videoEncodeNode);
			sourceNode.start();
			console.log("[publish] sourceNode started");

			setIsStreaming(true);
			console.log(`Started streaming from ${sourceType()}`);
		} catch (err) {
			const errorMessage = err instanceof Error ? err.message : String(err);
			setError(errorMessage);
			console.error("Failed to start streaming:", err);
		}

		// Debug: catch unhandled rejections in this function scope
		globalThis.addEventListener('unhandledrejection', (ev) => {
			console.error('[publish] unhandledrejection:', (ev as PromiseRejectionEvent).reason);
		});

		// Video metadata
		console.log("[publish] creating videoMetaStream/writer");
		const videoMetaStream = new TransformStream<VideoMetadata>(); // TODO: specify type
		const videoMetaWriter = videoMetaStream.writable.getWriter();
		// const videoMetaReader = videoMetaStream.readable.getReader();
		let videoMeta: VideoMetadata | undefined;

		// Seed initial video metadata so subscribers can configure decoder early.
		// This helps when encoder doesn't immediately expose decoderConfig on the first keyframe.
		try {
			console.log("[publish] seeding initial video.meta — building seedConfig");
			const seedConfig = await videoEncoderConfig({
				width: canvasWidth(),
				height: canvasHeight(),
				bitrate: 2_500_000,
				frameRate: 30,
				tryHardware: true,
			});
			const seedMeta = { ...(seedConfig as unknown as VideoDecoderConfig), startGroup: 0 } as unknown as VideoMetadata;
			try {
				console.log("[publish] writing seedMeta to videoMetaWriter:", seedMeta);
				await videoMetaWriter.write(seedMeta);
				console.log("[publish] seeded initial video.meta:", seedMeta);
			} catch (werr) {
				console.error("[publish] failed to seed initial video.meta:", werr);
			}
		} catch (err) {
			console.error("[publish] failed to build seed video config:", err);
		}

		// Audio metadata
		const audioMetaStream = new TransformStream<AudioMetadata>(); // TODO: specify type
		const audioMetaWriter = audioMetaStream.writable.getWriter();
		// const audioMetaReader = audioMetaStream.readable.getReader();
		let audioMeta: AudioMetadata | undefined;

		// Publish
		console.log("[publish] calling mux.publishFunc with broadcastPath=", broadcastPath);
		try {
			console.log("[publish] invoking mux.publishFunc now");
			mux.publishFunc(
				publishCtx.done(),
				broadcastPath,
				async (track) => {
					console.log("[publishFunc] Track handler called for:", track.trackName);
					switch (track.trackName) {
					case "video": {
						console.log("[publishFunc] Starting video track processing");
						if (!videoEncodeNode) {
							throw new Error("Encode node not initialized");
						}

						let currentGroup: GroupWriter | undefined = undefined;

						// Pass the track as the VideoEncodeDestination
						const { done } = videoEncodeNode.encodeTo({
							output: async (
								chunk: EncodedVideoChunk,
								decoderConfig?: VideoDecoderConfig,
							) => {
								switch (chunk.type) {
									case "key": {
										if (currentGroup) {
											void currentGroup.close();
										}
										const [group, err] = await track.openGroup();
										if (err) {
											return err;
										}
										currentGroup = group;

										if (decoderConfig) {										console.log("[publish] encoder provided decoderConfig:", decoderConfig);											videoMeta = {
												...decoderConfig,
												startGroup: currentGroup.sequence,
											};
										console.log("[publish] enqueueing video.meta:", videoMeta);
										await videoMetaWriter.write(videoMeta);
										console.log("[publish] video.meta written");
										}
										break;
									}
									case "delta": {
										if (!currentGroup) {
											// Drop delta frames until we get a keyframe
											return;
										}

										break;
									}
								}

								const frame = new MediaFrame(chunk);

								const err = await currentGroup.writeFrame(frame);
								if (err) {
									throw err;
								}
							},
						});

						await done;
						break;
					}

					// New: publish video metadata so subscribers can auto-configure decoder
					case "video.meta": {
						console.log("[publishFunc] Starting video.meta track");
						const reader = videoMetaStream.readable.getReader();
						try {
							while (true) {
								const { value: meta, done: streamDone } = await reader.read();
								if (streamDone) break;
								console.log("[publishFunc video.meta] received meta from stream:", meta);
								const json = JSON.stringify(meta);
								const buf = new TextEncoder().encode(json);

								const [group, openErr] = await track.openGroup();
								if (openErr) {
									console.error("video.meta: openGroup error", openErr);
									break;
								}

								const metaFrame = new MediaFrame({
									timestamp: Date.now() * 1000,
									byteLength: buf.byteLength,
									copyTo(target: ArrayBuffer | ArrayBufferView) {
										new Uint8Array(target as ArrayBuffer).set(buf);
									},
								});

								const writeErr = await group.writeFrame(metaFrame);
								if (writeErr) {
									console.error("video.meta: writeFrame error", writeErr);
								} else {
									console.log("[publishFunc video.meta] wrote meta frame (group=%d)", group.sequence);
								}
								void group.close();
							}
						} finally {
							reader.releaseLock?.();
						}
						break;
					}

					case "audio": {
						if (!audioEncodeNode) {
							throw new Error("Audio encode node not initialized");
						}

						const { done } = audioEncodeNode.encodeTo({
							output: async (
								chunk: EncodedAudioChunk,
								decoderConfig?: AudioDecoderConfig,
							) => {
								const [group, err] = await track.openGroup();
								if (err) {
									return err;
								}

								if (decoderConfig) {
									audioMeta = { ...decoderConfig, startGroup: group.sequence };
									await audioMetaWriter.write(audioMeta);
								}

								const writeErr = await group.writeFrame(new MediaFrame(chunk));
								if (writeErr) {
									// TODO: handle error
								}

								void group.close();
							},
						});

						await done;
						break;
					}
					default: {
						console.log("[publishFunc] Unknown track:", track.trackName, "- ignoring");
						return;
					}
				}
			},
		);

			// broadcast path announcement is taken care of by the mux; the session
			// object exposed to the page currently does not implement a public
			// `announce` method any more.  We retain the debug hook above but stop
			// trying to invoke it explicitly.
		} catch (err) {
			const errorMessage = err instanceof Error ? err.message : String(err);
			setError(errorMessage);
			console.error("[publish] streaming error:", err);
		}
	};

	const stopStreaming = () => {
		cancelPublish();
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
