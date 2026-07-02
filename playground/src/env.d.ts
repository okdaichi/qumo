/// <reference types="vite/client" />

interface ImportMetaEnv {
	readonly VITE_RELAY_URL: string;
	// SHA-256 hash of the relay's WebTransport cert (hex). Optional in dev —
	// when unset the demo surfaces remediation guidance in the UI.
	readonly VITE_CERT_HASH?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}
