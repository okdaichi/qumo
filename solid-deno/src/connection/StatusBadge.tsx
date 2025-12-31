import { useConnection } from "./store.tsx";

const statusColors: Record<string, string> = {
  disconnected: "#888",
  connecting: "#f59e0b",
  connected: "#10b981",
  error: "#ef4444",
};

const statusLabels: Record<string, string> = {
  disconnected: "Disconnected",
  connecting: "Connecting...",
  connected: "Connected",
  error: "Error",
};

export function StatusBadge() {
  const { state } = useConnection();

  return (
    <div
      class="status-badge"
      style={{
        display: "inline-flex",
        "align-items": "center",
        gap: "8px",
        padding: "4px 12px",
        "border-radius": "12px",
        background: "#1a1a1a",
        border: "1px solid #333",
      }}
    >
      <div
        class="status-indicator"
        style={{
          width: "8px",
          height: "8px",
          "border-radius": "50%",
          background: statusColors[state.status],
        }}
      />
      <span style={{ "font-size": "14px" }}>{statusLabels[state.status]}</span>
    </div>
  );
}
