/* @refresh reload */
import { render } from "solid-js/web";
import "./index.css";
import App from "./App.tsx";
import { exportLogs, getLevel, setLevel } from "./log.ts";

const root = document.getElementById("root");

// Dev-only console handle for ad-hoc debugging: change the log level at runtime
// and grab a transcript of recent logs to paste into a bug report. The DEV gate
// lets Vite strip this block from the production bundle.
if (import.meta.env.DEV) {
	(window as unknown as { qumoLogs: unknown }).qumoLogs = {
		// qumoLogs.setLevel("debug") — or per-tag: setLevel("trace", "subscribe.video")
		setLevel,
		getLevel,
		// Returns recent logs as text (pass { json: true } for ndjson).
		exportLogs,
	};
}

render(
	() => <App />,
	root!,
);
