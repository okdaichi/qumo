import { createSignal } from "solid-js";
import { connect, DefaultTrackMux } from "@qumo/moq";
import type { Session } from "@qumo/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import { createUsername } from "./user/user_name.ts";
import { UserController, UserProvider } from "./user/UserProvider.tsx";
import {
	type ConnectionState,
	ConnectionStatus,
	friendlyConnError,
} from "./ConnectionStatus.tsx";

export function Dashboard() {
	const { username, regenerate } = createUsername();
	const relayUrl = import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433";
	const certHash = import.meta.env.VITE_CERT_HASH as string | undefined;

	const transportOptions: WebTransportOptions = {};
	if (certHash) {
		const bytes = new Uint8Array(certHash.length / 2);
		for (let i = 0; i < certHash.length; i += 2) {
			bytes[i / 2] = parseInt(certHash.substring(i, i + 2), 16);
		}
		transportOptions.serverCertificateHashes = [
			{ algorithm: "sha-256", value: bytes },
		];
		console.log("[client] using cert hash:", certHash);
	} else {
		console.warn("[client] VITE_CERT_HASH not set — run 'mage cert' to generate");
	}

	const mux = DefaultTrackMux;

	// Transport lifecycle surfaced to the UI (issue #134). Starts "connecting"
	// the moment we dial; resolves to "connected"/"failed" when the session
	// promise settles.
	const [connState, setConnState] = createSignal<ConnectionState>("connecting");
	const [connError, setConnError] = createSignal<string | null>(null);

	const session: Promise<Session> = connect(relayUrl, { mux, transportOptions });
	session.then(
		(s) => {
			setConnState("connected");
			// Best-effort mid-session disconnect detection: if the Session exposes
			// a `closed` promise, surface its settlement so a relay killed
			// mid-broadcast is visible. Guarded so an older API never throws.
			const closed = (s as { closed?: unknown }).closed;
			if (closed && typeof (closed as Promise<unknown>).then === "function") {
				(closed as Promise<unknown>).then(
					() => {
						setConnError("Connection closed by the relay.");
						setConnState("failed");
					},
					(e) => {
						setConnError(e instanceof Error ? e.message : String(e));
						setConnState("failed");
					},
				);
			}
		},
		(e) => {
			setConnError(friendlyConnError(e, !certHash));
			setConnState("failed");
		},
	);

	return (
		<div class="dashboard">
			<div class="top-controls">
				<ConnectionStatus
					state={connState()}
					error={connError()}
					certHashMissing={!certHash}
				/>
				<div class="user-control">
					<UserController regenerate={regenerate} />
					<span class="username" title="Your session username">
						{username()}
					</span>
				</div>
			</div>

			<UserProvider username={username}>
				<div class="boards">
					<PublishBoard mux={mux} />
					<SubscribeBoard session={session} />
				</div>
			</UserProvider>
		</div>
	);
}
