import { type Accessor, createSignal, onCleanup } from "solid-js";
import { connect, DefaultTrackMux } from "@qumo/moq";
import type { Session } from "@qumo/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import {
	type ConnectionState,
	ConnectionStatus,
	friendlyConnError,
	sanitizeReason,
} from "./ConnectionStatus.tsx";
import { buildTransportOptions } from "./cert.ts";
import { relayUrlFor, type ScenarioId, SCENARIOS } from "./scenarios.ts";
import { PushInstructions } from "./PushInstructions.tsx";

// Owns one WebTransport session for the active scenario. Each scenario is a
// different origin, so the parent <Show> remounts this component (tearing down
// the old session via onCleanup) whenever the scenario changes.
export function ScenarioView(props: {
	scenario: ScenarioId;
	path: Accessor<string>;
}) {
	const scenario = SCENARIOS[props.scenario];
	const ingest = scenario.mode === "subscribe";

	const { transportOptions, problem: certHashProblem } = buildTransportOptions(
		import.meta.env.VITE_CERT_HASH,
	);

	const mux = DefaultTrackMux;
	const relayUrl = relayUrlFor(props.scenario);

	// Transport lifecycle surfaced to the UI (issue #134). Starts "connecting"
	// the moment we dial; moves to "connected" on a successful handshake. The
	// session's `closed` promise then surfaces a mid-session disconnect — it
	// resolves with a close info on a graceful (relay-initiated) close and
	// rejects on a transport error, so the two are distinguished.
	const [connState, setConnState] = createSignal<ConnectionState>("connecting");
	const [connError, setConnError] = createSignal<string | null>(null);

	const session: Promise<Session> = connect(relayUrl, { mux, transportOptions });
	session.then(
		(s) => {
			setConnState("connected");
			// Mid-session disconnect detection: closed resolves on graceful
			// close (-> "closed"), rejects on a transport error (-> "failed").
			s.closed.then(
				(info) => {
					setConnError(sanitizeReason(info.reason, "Connection closed by the relay."));
					setConnState("closed");
				},
				(e) => {
					setConnError(e instanceof Error ? e.message : String(e));
					setConnState("failed");
				},
			);
		},
		(e) => {
			setConnError(friendlyConnError(e, certHashProblem));
			setConnState("failed");
		},
	);

	// Drop the session when switching scenario (this view unmounts).
	onCleanup(() => {
		session.then((s) => s.close().catch(() => {})).catch(() => {});
	});

	return (
		<>
			<ConnectionStatus
				state={connState()}
				error={connError()}
				certHashProblem={certHashProblem}
			/>

			{ingest && <PushInstructions scenario={props.scenario} path={props.path} />}

			<div class={ingest ? "boards single" : "boards"}>
				{!ingest && <PublishBoard mux={mux} path={props.path} />}
				<SubscribeBoard session={session} path={props.path} />
			</div>
		</>
	);
}
