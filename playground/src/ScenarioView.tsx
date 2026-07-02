import { type Accessor, createSignal, onCleanup, onMount } from "solid-js";
import { connect, DefaultTrackMux } from "@qumo/moq";
import type { Session } from "@qumo/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import {
	type ConnectionState,
	ConnectionStatus,
	friendlyConnError,
} from "./ConnectionStatus.tsx";
import { sanitizeReason } from "./errors.ts";
import { buildTransportOptions, type CertHashProblem } from "./cert.ts";
import { getConfig } from "./config.ts";
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

	const mux = DefaultTrackMux;
	const relayUrl = relayUrlFor(props.scenario);

	// Transport lifecycle surfaced to the UI (issue #134). Starts "connecting"
	// the moment we dial; moves to "connected" on a successful handshake. The
	// session's `closed` promise then surfaces a mid-session disconnect — it
	// resolves with a close info on a graceful (relay-initiated) close and
	// rejects on a transport error, so the two are distinguished.
	const [connState, setConnState] = createSignal<ConnectionState>("connecting");
	const [connError, setConnError] = createSignal<string | null>(null);
	// The cert-hash problem (missing/malformed) is resolved after the runtime
	// config is fetched, so it's a signal read reactively by ConnectionStatus.
	const [certHashProblem, setCertHashProblem] = createSignal<CertHashProblem | null>(null);

	// Defer the dial until the runtime config (cert hash, served at /config by
	// `qumo playground`, or the VITE_CERT_HASH fallback in the `mage web` dev
	// path) is resolved. SubscribeBoard awaits props.session lazily, so a
	// deferred promise is safe: it resolves to whatever connect() returns once
	// config is ready.
	let dialSession!: (s: Promise<Session>) => void;
	const session: Promise<Session> = new Promise<Promise<Session>>((resolve) => {
		dialSession = resolve;
	}).then((s) => s);

	onMount(async () => {
		const cfg = await getConfig();
		const { transportOptions, problem } = buildTransportOptions(cfg.certHash);
		setCertHashProblem(problem);

		const connected = connect(relayUrl, { mux, transportOptions });
		dialSession(connected);

		connected.then(
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
						// Transport errors can carry opaque quic/TLS internals; strip
						// control chars and clamp the length before display.
						setConnError(
							sanitizeReason(
								e instanceof Error ? e.message : String(e),
								"Connection failed",
							),
						);
						setConnState("failed");
					},
				);
			},
			(e) => {
				setConnError(friendlyConnError(e, problem));
				setConnState("failed");
			},
		);
	});

	// Drop the session when switching scenario (this view unmounts).
	onCleanup(() => {
		session.then((s) => s.close().catch(() => {})).catch(() => {});
	});

	return (
		<>
			<ConnectionStatus
				state={connState()}
				error={connError()}
				certHashProblem={certHashProblem()}
			/>

			{ingest && <PushInstructions scenario={props.scenario} path={props.path} />}

			<div class={ingest ? "boards single" : "boards"}>
				{!ingest && <PublishBoard mux={mux} path={props.path} />}
				<SubscribeBoard session={session} path={props.path} />
			</div>
		</>
	);
}
