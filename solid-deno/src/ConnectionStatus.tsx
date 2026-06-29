import { Show } from "solid-js";

// WebTransport session lifecycle as surfaced to the user (issue #134).
// "connecting" until the session promise settles; "connected" on success;
// "failed" with a concise reason on rejection or mid-session close.
export type ConnectionState = "connecting" | "connected" | "failed";

const LABELS: Record<ConnectionState, string> = {
	connecting: "Connecting to relay…",
	connected: "Connected to relay",
	failed: "Connection failed",
};

// Connection status indicator + actionable guidance.
//
// - Always shows the live transport state (connecting / connected / failed).
// - On failure, shows a concise, user-facing reason.
// - When the cert hash is missing, shows the remediation step up front — a
//   self-signed relay cert will be rejected by WebTransport without it, so a
//   first-time user otherwise sees only a silent console.warn.
export function ConnectionStatus(props: {
	state: ConnectionState;
	error: string | null;
	certHashMissing: boolean;
}) {
	return (
		<div class="connection-status" data-state={props.state}>
			<span class="status-dot" />
			<span class="status-label">{LABELS[props.state]}</span>

			<Show when={props.state === "failed" && props.error}>
				<span class="status-reason">{props.error}</span>
			</Show>

			<Show when={props.certHashMissing}>
				<span class="status-warn">
					Certificate hash not set — WebTransport will reject the relay's
					self-signed cert. Run <code>mage cert</code> and set{" "}
					<code>VITE_CERT_HASH</code> in <code>solid-deno/.env</code>.
				</span>
			</Show>
		</div>
	);
}

// Map a raw WebTransport connect failure to a concise, actionable message.
// The cert-hash case is called out specifically because it is by far the most
// common first-run failure and has a one-command fix.
export function friendlyConnError(err: unknown, certHashMissing: boolean): string {
	if (certHashMissing) {
		return "No certificate hash configured — WebTransport rejected the relay's self-signed cert. Run 'mage cert' and set VITE_CERT_HASH.";
	}
	const raw = err instanceof Error ? err.message : String(err);
	return `Could not connect to the relay: ${raw}`;
}
