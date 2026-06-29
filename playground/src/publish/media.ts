export type MediaSourceType = "camera" | "screen";

export const getMediaStream = async (type: MediaSourceType): Promise<MediaStream> => {
	try {
		switch (type) {
			case "camera":
				return await navigator.mediaDevices.getUserMedia({
					video: {
						width: { ideal: 1280 },
						height: { ideal: 720 },
						frameRate: { ideal: 30 },
					},
					audio: true,
				});
			case "screen":
				return await navigator.mediaDevices.getDisplayMedia({
					video: {
						width: { ideal: 1920 },
						height: { ideal: 1080 },
						frameRate: { ideal: 30 },
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
