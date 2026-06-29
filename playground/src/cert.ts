// Cert-hash parsing + WebTransport transport options for the demo.
// Shared by every scenario (all origins use the same `mage cert` cert).

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
	const problem: CertHashProblem | null = "bytes" in parsed ? null : parsed.problem;
	if (problem) {
		console.warn(
			`[client] VITE_CERT_HASH ${problem} — run 'mage cert' to generate`,
		);
	}
	return { transportOptions, problem };
}
