import { createSignal, Show, onMount } from "solid-js";
import { useUser } from "../user/context.ts";
import { DefaultTrackMux, type BroadcastPath } from "@okdaichi/moq"
import { VideoContext, VideoEncodeNode, MediaStreamVideoSourceNode, AudioEncodeNode } from "@okdaichi/av-nodes";
import { getMediaStream, type MediaSourceType } from "./media.ts";
import { background, withCancel, type CancelFunc, type Context } from "jsr:@okdaichi/golikejs@~0.6.1/context";

const GOP_DURATION = 2000; // 2 seconds


export function PublishBoard(_props: {}) {
    const user = useUser()
    const broadcastPath: BroadcastPath = `/${user.name()}`;

    const [sourceType, setSourceType] = createSignal<MediaSourceType>("camera");
    const [isStreaming, setIsStreaming] = createSignal(false);
    const [error, setError] = createSignal<string | null>(null);

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
                    if (timestamp - lastKeyframeTime >= GOP_DURATION) {
                        lastKeyframeTime = timestamp;
                        return true;
                    }
                    return false;
                }
            });
        }
    });

    let publishCtx: Context;
    let cancelPublish: CancelFunc;

    const startStreaming = async () => {
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

            setIsStreaming(true);
            console.log(`Started streaming from ${sourceType()}`);
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : String(err);
            setError(errorMessage);
            console.error("Failed to start streaming:", err);
        }

        // Publish
        void DefaultTrackMux.publishFunc(
            publishCtx.done(),
            broadcastPath,
            async (track) => {
                switch (track.trackName) {
                    case "video": {
                        if (!videoEncodeNode) {
                            throw new Error("Encode node not initialized");
                        }
                        // Pass the track as the VideoEncodeDestination
                        const { done } = videoEncodeNode.encodeTo({
                            output: (chunk) => {
                                return undefined;
                            },
                            done: publishCtx.done(),
                        });

                        return await done;
                    }
                    case "audio": {
                        if (!audioEncodeNode) {
                            throw new Error("Audio encode node not initialized");
                        }

                        const { done } = audioEncodeNode.encodeTo({
                            output: (chunk) => {
                                return undefined;
                            },
                            done: publishCtx.done(),
                        });

                        return await done;
                    }
                    default: {
                        return;
                    }
                }
            }
        );
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
                    width="1280"
                    height="720"
                    style={{ 
                        width: "100%", 
                        "max-width": "800px",
                        height: "auto",
                        border: "1px solid #ccc",
                        "border-radius": "8px",
                        background: "#000"
                    }}
                />
            </div>
        </div>
    );
}
