import { Show } from "solid-js";
import type { CertHashProblem } from "./cert.ts";

// WebTransport session lifecycle as surfaced to the user (issue #134).
// "connecting" until the connect() promise settles; "connected" on success;
// "closed" when the relay ends the session gracefully mid-stream; "failed"
// with a concise reason on a handshake rejection or transport error.
export type ConnectionState = "connecting" | "connected" | "closed" | "failed";

const LABELS: Record<ConnectionState, string> = {
	connecting: "Connecting to relay…",
	connected: "Connected to relay",
	closed: "Connection closed",
	failed: "Connection failed",
};

// User-facing guidance shown whenever the cert hash can't pin the relay cert.
const CERT_WARN: Record<CertHashProblem, string> = {
	missing: "Certificate hash not set — WebTransport will reject the relay's self-signed cert.",
	malformed:
		"VITE_CERT_HASH is malformed (expected 64 hex chars) — WebTransport can't pin the relay cert.",
};

// Connection status indicator: live transport state (dot + label), a concise
// failure reason, and up-front remediation when the cert hash is missing or
// malformed.
export function ConnectionStatus(props: {
	state: ConnectionState;
	error: string | null;
	certHashProblem: CertHashProblem | null;
}) {
	return (
		<div class="connection-status" data-state={props.state}>
			<span class="status-dot" />
			<span class="status-label">{LABELS[props.state]}</span>

			<Show when={(props.state === "failed" || props.state === "closed") && props.error}>
				<span class="status-reason">{props.error}</span>
			</Show>

			{props.certHashProblem && (
				<span class="status-warn">
					{CERT_WARN[props.certHashProblem]} Run <code>mage cert</code> and set{" "}
					<code>VITE_CERT_HASH</code> in <code>playground/.env</code>.
				</span>
			)}
		</div>
	);
}

// Map a raw WebTransport connect failure to a concise, actionable message.
// Returns null when the failure is cert-related — ConnectionStatus already
// shows the cert remediation, so a duplicate reason would only clutter the bar.
export function friendlyConnError(
	err: unknown,
	certHashProblem: CertHashProblem | null,
): string | null {
	if (certHashProblem) return null;
	const raw = err instanceof Error ? err.message : String(err);
	return `Could not connect to the relay: ${raw}`;
}

// Defensively clean a relay-provided close reason before display. SolidJS
// escapes text interpolation (so there's no HTML-injection sink), but the relay
// is an untrusted peer — strip control characters and clamp the length so it
// can't flood the status bar. Falls back to `fallback` when nothing remains.
export function sanitizeReason(reason: string | undefined, fallback: string): string {
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
