import { createSignal, Show } from "solid-js";
import { useConnection } from "./store.tsx";
import { useConfig } from "../config.tsx";

export function ConnectionPanel() {
  const { config } = useConfig();
  const { state, connect, disconnect } = useConnection();
  const [url, setUrl] = createSignal(config.relayUrl);

  const handleConnect = async () => {
    try {
      await connect(url());
    } catch (error) {
      console.error("Connection failed:", error);
    }
  };

  const handleDisconnect = async () => {
    try {
      await disconnect();
    } catch (error) {
      console.error("Disconnect failed:", error);
    }
  };

  return (
    <div class="connection-panel">
      <h3>Connection</h3>
      
      <div class="input-group">
        <label>Relay URL:</label>
        <input
          type="text"
          value={url()}
          onInput={(e) => setUrl(e.currentTarget.value)}
          disabled={state.status !== "disconnected"}
        />
      </div>

      <div class="button-group">
        <Show
          when={state.status === "disconnected"}
          fallback={
            <button onClick={handleDisconnect} disabled={state.status === "connecting"}>
              Disconnect
            </button>
          }
        >
          <button onClick={handleConnect}>Connect</button>
        </Show>
      </div>

      <Show when={state.error}>
        <div class="error-message">{state.error}</div>
      </Show>
    </div>
  );
}
