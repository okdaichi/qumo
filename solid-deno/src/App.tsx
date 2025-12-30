import { useConfig } from './context/index.ts'
import './App.css'

function App() {
  const config = useConfig()

  return (
    <div class="app">
      <h1>{config.appName}</h1>
      <div class="card">
        <p>Relay URL: <code>{config.relayUrl}</code></p>
        <p>API URL: <code>{config.apiUrl}</code></p>
        <p>Mode: <code>{config.isDev ? 'Development' : 'Production'}</code></p>
      </div>
    </div>
  )
}

export default App
