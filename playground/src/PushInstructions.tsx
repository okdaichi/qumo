import { createSignal, Show } from "solid-js";
import { type ScenarioId, SCENARIOS } from "./scenarios.ts";

// Copy-pasteable ffmpeg push command for ingest scenarios (RTMP/RTSP), so a
// user knows exactly how to feed the stream they're about to subscribe to.
export function PushInstructions(props: { scenario: ScenarioId }) {
	const scenario = SCENARIOS[props.scenario];
	const cmd = scenario.pushCommand;
	const [copied, setCopied] = createSignal(false);

	const copy = () => {
		if (!cmd) return;
		navigator.clipboard?.writeText(cmd).then(() => {
			setCopied(true);
			setTimeout(() => setCopied(false), 1200);
		}).catch(() => {});
	};

	return (
		<Show when={cmd && scenario.pushTarget}>
			<div class="push-instructions">
				<div class="push-instructions-head">
					<span>
						Push a stream to <code>{scenario.pushTarget}</code>, then start subscribing:
					</span>
					<button type="button" class="copy-btn" onClick={copy}>
						{copied() ? "Copied" : "Copy"}
					</button>
				</div>
				<pre>
					<code>{cmd}</code>
				</pre>
			</div>
		</Show>
	);
}
