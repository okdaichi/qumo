// Connection status
export type ConnectionStatus = "disconnected" | "connecting" | "connected" | "error";

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

// Connection state
export interface ConnectionState {
  status: ConnectionStatus;
  error?: string;
}

// Health status (from server API)
export interface HealthStatus {
  status: "healthy" | "degraded" | "unhealthy";
  timestamp: string;
  uptime: string;
  active_connections: number;
  upstream_connected: boolean;
  version?: string;
}

// Track information
export interface TrackInfo {
  name: string;
  type: "video" | "audio";
  status: "active" | "paused" | "ended";
}
