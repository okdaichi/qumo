import { SubscribeErrorCode } from "@qumo/moq";

// Friendly, actionable error messages for the demo (issue #138).
//
// The publish/subscribe boards used to surface bare "Error: <message>" text —
// often an opaque DOMException string or a MoQ error code the user can't act
// on. This module maps the common, recognizable failure cases to a short
// sentence that tells the user what went wrong and what to do next. Anything
// it can't classify falls back to a cleaned-up first line of the raw message
// (no stack traces, no control characters).
//
// Keep the messages self-contained — the UI renders them verbatim, without a
// leading "Error:" prefix.

// Reduce a raw message to a single safe line: take the first line, drop a
// leading "Error:" wrapper, trim, and clamp the length.
function cleanRaw(input: string): string {
	const firstLine = input.split("\n")[0]?.trim() ?? "";
	const stripped = firstLine.replace(/^error:\s*/i, "");
	return stripped.slice(0, 200);
}

// MoQ subscribe/track errors carry their numeric code on `.code`. It's a
// plain number on the wire, so read it defensively rather than isinstance.
function moqCode(err: unknown): number | undefined {
	const code = (err as { code?: unknown })?.code;
	return typeof code === "number" ? code : undefined;
}

// Map a thrown error to a friendly, actionable message.
export function friendlyMessage(err: unknown): string {
	const raw = err instanceof Error ? err.message : String(err);
	const name = (err as { name?: string } | null)?.name;

	// Camera / microphone / screen-share permission and device failures.
	// These reach us as DOMExceptions from getUserMedia / getDisplayMedia;
	// media.ts rethrows them unchanged so the name survives.
	switch (name) {
		case "NotAllowedError":
		case "SecurityError":
			return "Camera or microphone access was denied. Allow access in your browser's site permissions, then click Start again.";
		case "NotFoundError":
			return "No camera or microphone was found. Connect a device and try again.";
		case "NotReadableError":
			return "Your camera or microphone is busy. Close the other app using it, then try again.";
		case "OverconstrainedError":
			return "The selected quality isn't supported by your camera. Try a lower resolution or framerate.";
		case "AbortError":
			return "Media access was cancelled. Click Start to try again.";
		case "NotSupportedError":
			// Most often an unsupported codec from the WebCodecs encoder/decoder.
			if (/codec|encod|decod|avc|hev1|h264|opus/i.test(raw)) {
				return "This video or audio codec isn't supported by your browser. Try a lower resolution or a different browser.";
			}
			return "This operation isn't supported by your browser.";
	}

	// MoQ subscribe failures. TrackNotFound is the common one for a demo:
	// the subscriber asked for a path nobody is publishing to.
	const code = moqCode(err);
	if (code !== undefined) {
		if (code === SubscribeErrorCode.TrackNotFound) {
			return "No stream at this path yet. Make sure the publisher — or the RTMP/RTSP pusher — is running, then click Start again.";
		}
		if (code === SubscribeErrorCode.Unauthorized) {
			return "The relay refused this stream as unauthorized.";
		}
		if (code === SubscribeErrorCode.SubscribeTimeout) {
			return "Timed out waiting for the stream. Make sure the publisher is running, then try again.";
		}
	}

	// WebTransport / TLS handshake noise from the transport layer.
	if (/webtransport|certificate|cert hash|tls|handshake|quic/i.test(raw)) {
		return "Could not connect to the relay. Check that it's running and that VITE_CERT_HASH is set, then reload.";
	}

	const cleaned = cleanRaw(raw);
	return cleaned || "Something went wrong. Check the browser console for details.";
}
