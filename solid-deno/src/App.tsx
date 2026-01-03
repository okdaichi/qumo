// import { config } from './config.tsx'
import "./App.css";
import { Dashborad } from "./Dashborad.tsx";

function App() {
	return (
		<div class="app">
			<header>
				<h1>{import.meta.env.VITE_APP_NAME}</h1>
			</header>

			<Dashborad/>

			<div class="card">
				<h3>Configuration</h3>
				<p>
					Relay URL: <code>{import.meta.env.VITE_RELAY_URL}</code>
				</p>
				<p>
					API URL: <code>{import.meta.env.VITE_HEALTH_URL}</code>
				</p>
				<p>
					Mode: <code>{import.meta.env.DEV ? "Development" : "Production"}</code>
				</p>
				<div style={{ "margin-top": "16px" }}>
					{/* <MoqPanel /> */}
				</div>
			</div>
		</div>
	);
}

export default App;
