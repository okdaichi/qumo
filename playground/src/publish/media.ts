export type MediaSourceType = "camera" | "screen";

export interface MediaConstraints {
	width?: number;
	height?: number;
	frameRate?: number;
}

export const getMediaStream = async (
	type: MediaSourceType,
	constraints?: MediaConstraints,
): Promise<MediaStream> => {
	// Apply caller-provided constraints as `ideal` hints. Defaults match the
	// pre-control behavior (720p@30 camera, 1080p@30 screen).
	const w = constraints?.width;
	const h = constraints?.height;
	const fps = constraints?.frameRate;
	try {
		switch (type) {
			case "camera":
				return await navigator.mediaDevices.getUserMedia({
					video: {
						width: { ideal: w ?? 1280 },
						height: { ideal: h ?? 720 },
						frameRate: { ideal: fps ?? 30 },
					},
					audio: true,
				});
			case "screen":
				return await navigator.mediaDevices.getDisplayMedia({
					video: {
						width: { ideal: w ?? 1920 },
						height: { ideal: h ?? 1080 },
						frameRate: { ideal: fps ?? 30 },
					},
					audio: true,
				});
			default:
				throw new Error(`Unsupported media source type: ${type}`);
		}
	} catch (err) {
		throw new Error(
			`Failed to get ${type} media: ${err instanceof Error ? err.message : String(err)}`,
		);
	}
};
