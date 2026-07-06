// Cert-hash parsing + WebTransport transport options for the demo.
// Shared by every scenario (all origins use the same `mage cert` cert).

import { createLogger } from "./log.ts";

const log = createLogger("cert");

// Why the cert hash can't pin the relay cert: not configured, or present but
// not a valid 64-char hex SHA-256.
export type CertHashProblem = "missing" | "malformed";

type ParsedCertHash =
	| { bytes: Uint8Array<ArrayBuffer> }
	| { problem: CertHashProblem };

// Parse the hex SHA-256 from VITE_CERT_HASH into the 32 bytes WebTransport pins.
// Tolerates surrounding whitespace and an optional 0x prefix, and rejects
// anything that isn't exactly 64 hex chars so a malformed value can't silently
// produce a wrong/too-short hash (which WebTransport would reject generically,
// hiding the real cause).
export function parseCertHash(raw: string | undefined): ParsedCertHash {
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

// Build WebTransport transport options from VITE_CERT_HASH. Returns the pinned
// hash bytes (when usable) plus a problem label for UI remediation otherwise.
//
// "missing" is intentionally NOT reported as a problem: when the cert is signed
// by mkcert (browser-trusted root CA) no pin is needed, so VITE_CERT_HASH is
// deliberately unset. On the self-signed fallback a genuinely-forgotten
// `mage cert` surfaces as a connection error via friendlyConnError instead.
// Only a malformed hash (the user set one but it isn't valid hex) is flagged.
export function buildTransportOptions(certHash: string | undefined): {
	transportOptions: WebTransportOptions;
	problem: CertHashProblem | null;
} {
	const parsed = parseCertHash(certHash);
	const transportOptions: WebTransportOptions = {};
	if ("bytes" in parsed) {
		transportOptions.serverCertificateHashes = [
			{ algorithm: "sha-256", value: parsed.bytes },
		];
	}
	const problem: CertHashProblem | null = "problem" in parsed && parsed.problem === "malformed"
		? "malformed"
		: null;
	if (problem) {
		log.warn("VITE_CERT_HASH is malformed (expected 64 hex chars)");
	}
	return { transportOptions, problem };
}
