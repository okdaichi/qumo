import "./App.css";
import { Dashboard } from "./Dashboard.tsx";

function App() {
	return (
		<div class="app">
			<header class="app-header">
				<h1>qumo</h1>
				<p class="app-subtitle">AV live streaming over MoQ / WebTransport</p>
			</header>
			<Dashboard />
		</div>
	);
}

export default App;
