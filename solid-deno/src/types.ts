// Media source types
export type MediaSource = "camera" | "screen";

// Publish configuration
export interface PublishConfig {
	source: MediaSource;
	trackName: string;
	width?: number;
	height?: number;
	frameRate?: number;
}

// Subscribe configuration
export interface SubscribeConfig {
	trackName: string;
}

// Health status (from server API)
export interface HealthStatus {
	status: "healthy" | "unhealthy";
	timestamp: string;
	uptime: string;
	active_connections: number;
	version?: string;
}

// Track information
export interface TrackInfo {
	name: string;
	type: "video" | "audio";
	status: "active" | "paused" | "ended";
}
