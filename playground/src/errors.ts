import { SubscribeErrorCode } from "@qumo/moq";
import type { MediaSourceType } from "./publish/media.ts";

// Friendly, actionable error messages for the demo (issue #138).
//
// The publish/subscribe boards used to surface bare "Error: <message>" text —
// often an opaque DOMException string or a MoQ internal message the user can't
// act on. This module maps the common, recognizable failure cases to a short
// sentence that tells the user what went wrong and what to do next. Anything
// it can't classify falls back to a cleaned-up first line of the raw message
// (no stack traces, no control characters).
//
// Keep the messages self-contained — the UI renders them verbatim, without a
// leading "Error:" prefix.

// Defensively clean an untrusted/free-form reason before display. SolidJS
// escapes text interpolation (so there's no HTML-injection sink), but relay
// reasons and library messages can still contain control characters or run
// long — strip the control chars and clamp the length so they can't flood the
// status bar. Falls back to `fallback` when nothing remains.
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

// Reduce a raw message to a single safe line: take the first line, drop a
// leading "Error:" wrapper, then strip control characters and clamp the length
// via sanitizeReason (so the "no control characters" promise above holds).
function cleanRaw(input: string): string {
	const firstLine = input.split("\n")[0]?.trim() ?? "";
	const stripped = firstLine.replace(/^error:\s*/i, "");
	return sanitizeReason(stripped, "");
}

// MoQ stream errors surface as a WebTransportStreamError carrying the peer's
// reset code on `.code` (the relay resets the subscribe stream with the MoQ
// SubscribeErrorCode — e.g. TrackNotFound — rather than sending a SUBSCRIBE_ERR
// message). It's a plain number, so read it defensively rather than instanceof.
function moqCode(err: unknown): number | undefined {
	const code = (err as { code?: unknown })?.code;
	return typeof code === "number" ? code : undefined;
}

// Map a thrown error to a friendly, actionable message. `source` (when known)
// distinguishes a camera/microphone failure from a screen-share failure, since
// getUserMedia and getDisplayMedia throw the same DOMException names.
export function friendlyMessage(err: unknown, source?: MediaSourceType): string {
	const raw = err instanceof Error ? err.message : String(err);
	const name = (err as { name?: string } | null)?.name;

	// Camera / microphone / screen-share permission and device failures.
	// These reach us as DOMExceptions from getUserMedia / getDisplayMedia;
	// media.ts rethrows them unchanged so the name survives.
	switch (name) {
		case "NotAllowedError":
		case "SecurityError":
			return source === "screen"
				? "Screen-share access was denied. Allow it in your browser's site permissions, then click Start again."
				: "Camera or microphone access was denied. Allow access in your browser's site permissions, then click Start again.";
		case "NotFoundError":
			return source === "screen"
				? "No screen or window was shared. Click Start and pick one to share."
				: "No camera or microphone was found. Connect a device and try again.";
		case "NotReadableError":
			return "Your camera, microphone, or screen is busy. Close the other app using it, then try again.";
		case "OverconstrainedError":
			return "The selected quality isn't supported by your device. Try a lower resolution or framerate.";
		case "AbortError":
			return "Media access was cancelled. Click Start to try again.";
		case "NotSupportedError":
			// Most often an unsupported codec from the WebCodecs encoder/decoder.
			if (/codec|encod|decod|avc|hev1|h264|opus/i.test(raw)) {
				return "This video or audio codec isn't supported by your browser. Try a lower resolution or a different browser.";
			}
			return "This operation isn't supported by your browser.";
	}

	// MoQ subscribe failures. The relay resets the subscribe stream with the MoQ
	// SubscribeErrorCode, which @qumo/moq surfaces as a WebTransportStreamError
	// whose `.code` is that code. TrackNotFound is the common demo case: the
	// subscriber asked for a path nobody is publishing to.
	const noStreamYet =
		"No stream at this path yet. Make sure the publisher — or the RTMP/RTSP pusher — is running, then click Start again.";
	const code = moqCode(err);
	if (code !== undefined) {
		if (code === SubscribeErrorCode.TrackNotFound) {
			return noStreamYet;
		}
		if (code === SubscribeErrorCode.Unauthorized) {
			return "The relay refused this stream as unauthorized.";
		}
		if (code === SubscribeErrorCode.SubscribeTimeout) {
			return "Timed out waiting for the stream. Make sure the publisher is running, then try again.";
		}
	}

	// A subscribe stream the peer reset before sending any response. When the
	// relay resets without a MoQ code (or the code is lost), @qumo/moq surfaces
	// it as a bare "WebTransportError: Received RESET_STREAM" wrapped in a
	// "failed to read SUBSCRIBE response type" message — no `.code` to classify.
	// In the subscribe context this still means "nobody is publishing to this
	// path", so map it to the same actionable text as TrackNotFound rather than
	// the generic connection-failure fallback (the relay IS reachable).
	if (/subscri/i.test(raw) && /reset_stream/i.test(raw)) {
		return noStreamYet;
	}

	// WebTransport / TLS handshake noise from the transport layer.
	if (/webtransport|certificate|cert hash|tls|handshake|quic/i.test(raw)) {
		return "Could not connect to the relay. Check that it's running and reload the page.";
	}

	const cleaned = cleanRaw(raw);
	return cleaned || "Something went wrong. Check the browser console for details.";
}
