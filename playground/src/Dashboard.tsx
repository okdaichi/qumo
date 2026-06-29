import { createEffect, createSignal, Show } from "solid-js";
import { ScenarioPicker } from "./ScenarioPicker.tsx";
import { PathControl } from "./PathControl.tsx";
import { ScenarioView } from "./ScenarioView.tsx";
import { isScenarioId, type ScenarioId, SCENARIOS } from "./scenarios.ts";
import { generateUsername } from "./user/user_name.ts";

// Read ?scenario= and ?path= from the URL (shareable deep links). Falls back to
// "echo" for an unknown/missing scenario.
function readParams(): { scenario: ScenarioId; path: string | null } {
	const params = new URLSearchParams(window.location.search);
	const s = params.get("scenario");
	return { scenario: s && isScenarioId(s) ? s : "echo", path: params.get("path") };
}

// Echo gets a random unique path so two peers meet only when sharing the link;
// ingest scenarios subscribe to the externally-pushed path.
function defaultPathFor(scenario: ScenarioId): string {
	return scenario === "echo" ? `/${generateUsername()}` : SCENARIOS[scenario].defaultPath;
}

export function Dashboard() {
	const initial = readParams();
	const [scenario, setScenario] = createSignal<ScenarioId>(initial.scenario);
	const [path, setPath] = createSignal<string>(initial.path ?? defaultPathFor(initial.scenario));

	// Keep the URL in sync so the current scenario+path is shareable as-is.
	createEffect(() => {
		const params = new URLSearchParams({ scenario: scenario(), path: path() });
		window.history.replaceState(null, "", `?${params.toString()}`);
	});

	// Switching scenario resets the path to that scenario's default — paths are
	// scenario-specific (echo = random name, ingest = /live/demo). User edits
	// within a scenario are preserved until they switch.
	const selectScenario = (id: ScenarioId) => {
		if (id === scenario()) return;
		setScenario(id);
		setPath(defaultPathFor(id));
	};

	return (
		<div class="dashboard">
			<div class="top-controls">
				<ScenarioPicker scenario={scenario()} onPick={selectScenario} />
				<PathControl scenario={scenario()} path={path} setPath={setPath} />
			</div>

			{
				/* Keyed remount: each scenario is a different WebTransport origin, so the
			    session/connection-status/boards rebuild on switch. */
			}
			<Show when={scenario()} keyed>
				<ScenarioView scenario={scenario()} path={path} />
			</Show>
		</div>
	);
}
