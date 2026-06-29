import { createSignal } from "solid-js";
import { connect, DefaultTrackMux } from "@qumo/moq";
import type { Session } from "@qumo/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import { createUsername } from "./user/user_name.ts";
import { UserController, UserProvider } from "./user/UserProvider.tsx";
import { type ConnectionState, ConnectionStatus, friendlyConnError } from "./ConnectionStatus.tsx";

// Defensively clean a relay-provided close reason before display. SolidJS
// escapes text interpolation (so there's no HTML-injection sink), but the relay
// is an untrusted peer — strip control characters and clamp the length so it
// can't flood the status bar. Falls back to `fallback` when nothing remains.
function sanitizeReason(reason: string | undefined, fallback: string): string {
	// Keep only printable chars (drop C0 controls 0x00-0x1f and DEL 0x7f);
	// expressed via codepoints to avoid escape sequences in source.
	const isPrintable = (ch: string) => {
		const c = ch.codePointAt(0)!;
		return c >= 0x20 && c !== 0x7f;
	};
	const cleaned = Array.from(reason ?? "")
		.filter(isPrintable)
		.join("")
		.trim()
		.slice(0, 200);
	return cleaned || fallback;
}

// Parse the hex SHA-256 from VITE_CERT_HASH into the 32 bytes WebTransport pins.
// Tolerates surrounding whitespace and an optional 0x prefix, and rejects
// anything that isn't exactly 64 hex chars so a malformed value can't silently
// produce a wrong/too-short hash (which WebTransport would reject generically,
// hiding the real cause).
type ParsedCertHash =
	| { bytes: Uint8Array<ArrayBuffer> }
	| { problem: "missing" | "malformed" };

function parseCertHash(raw: string | undefined): ParsedCertHash {
	const hex = (raw ?? "").trim().replace(/^0x/i, "");
	if (hex === "") return { problem: "missing" };
	if (hex.length !== 64 || !/^[0-9a-fA-F]+$/.test(hex)) {
		return { problem: "malformed" };
	}
	const bytes = new Uint8Array(32);
	for (let i = 0; i < 32; i++) {
		bytes[i] = parseInt(hex.substring(i * 2, i * 2 + 2), 16);
	}
	return { bytes };
}

export function Dashboard() {
	const { username, regenerate } = createUsername();
	const relayUrl = import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433";
	const certHash = import.meta.env.VITE_CERT_HASH;
	const parsedHash = parseCertHash(certHash);
	// null when we have usable hash bytes; otherwise the reason the cert hash
	// can't pin the relay cert (missing or malformed).
	const certHashProblem: "missing" | "malformed" | null = "bytes" in parsedHash
		? null
		: parsedHash.problem;

	const transportOptions: WebTransportOptions = {};
	if ("bytes" in parsedHash) {
		transportOptions.serverCertificateHashes = [
			{ algorithm: "sha-256", value: parsedHash.bytes },
		];
	} else {
		console.warn(
			`[client] VITE_CERT_HASH ${parsedHash.problem} — run 'mage cert' to generate`,
		);
	}

	const mux = DefaultTrackMux;

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

	return (
		<div class="dashboard">
			<div class="top-controls">
				<ConnectionStatus
					state={connState()}
					error={connError()}
					certHashProblem={certHashProblem}
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
