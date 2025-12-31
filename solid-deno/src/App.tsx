import { useConfig } from './config.tsx'
import { UserBadge } from './user/UserBadge.tsx'
import { StatusBadge } from './connection/StatusBadge.tsx'
import { useConnection } from './connection/store.tsx'
import './App.css'

function App() {
  const { config, setConfig } = useConfig()
  const { state, connect, disconnect } = useConnection()

  const handleConnect = async () => {
    try {
      await connect(config.relayUrl)
    } catch (error) {
      console.error("Connection failed:", error)
    }
  }

  const handleDisconnect = async () => {
    try {
      await disconnect()
    } catch (error) {
      console.error("Disconnect failed:", error)
    }
  }

  return (
    <div class="app">
      <header>
        <h1>{config.appName}</h1>
        <div style={{ display: "flex", gap: "16px", "align-items": "center" }}>
          <UserBadge />
          <StatusBadge />
        </div>
      </header>

      <div class="card">
        <h3>Configuration</h3>
        <label>
          Relay URL:
          <input
            type="text"
            value={config.relayUrl}
            onInput={e => setConfig("relayUrl", e.currentTarget.value)}
            style={{ width: "320px" }}
          />
        </label>
        <p>API URL: <code>{config.apiUrl}</code></p>
        <p>Mode: <code>{config.isDev ? 'Development' : 'Production'}</code></p>
        <div style={{ "margin-top": "16px" }}>
          {state.status === "disconnected" ? (
            <button type="button" onClick={handleConnect}>Connect</button>
          ) : (
            <button type="button" onClick={handleDisconnect} disabled={state.status === "connecting"}>
              Disconnect
            </button>
          )}
        </div>
        {state.error && (
          <div class="error-message">{state.error}</div>
        )}
      </div>
    </div>
  )
}

export default App
