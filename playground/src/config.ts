// Runtime configuration for the playground.
//
// Two code paths share this module:
//   - `qumo playground` (distribution path) serves a `/config` JSON endpoint
//     next to the UI; getConfig() fetches it so the dev cert hash is never
//     baked into the bundle and the UI never needs rebuilding when the cert
//     changes.
//   - `mage web` (Vite dev path) has no `/config` endpoint, so the fetch fails
//     and getConfig() falls back to the same import.meta.env values used before
//     this module existed — preserving the developer workflow unchanged.

export interface ResolvedConfig {
	/** https URL the browser dials over WebTransport, e.g. https://localhost:4433. */
	relayUrl: string;
	/** SHA-256 (hex) of the relay's WebTransport cert, or undefined when unset. */
	certHash?: string;
}

let pending: Promise<ResolvedConfig> | null = null;

/** getConfig resolves the runtime config once and caches it for the session. */
export function getConfig(): Promise<ResolvedConfig> {
	if (!pending) {
		pending = resolveConfig();
	}
	return pending;
}

async function resolveConfig(): Promise<ResolvedConfig> {
	try {
		const res = await fetch("/config", {
			headers: { Accept: "application/json" },
		});
		if (res.ok) {
			const cfg = (await res.json()) as Partial<ResolvedConfig>;
			return {
				relayUrl: cfg.relayUrl ?? "https://localhost:4433",
				certHash: cfg.certHash,
			};
		}
	} catch {
		// Dev path (Vite dev server, no /config) — fall through to env vars.
	}
	return envFallback();
}

function envFallback(): ResolvedConfig {
	return {
		relayUrl: import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433",
		certHash: import.meta.env.VITE_CERT_HASH,
	};
}
