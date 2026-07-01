package playground

// Config is the runtime configuration payload served at /config and consumed by
// the frontend on startup. It replaces the build-time VITE_* env vars so the UI
// never needs rebuilding when the dev cert changes.
type Config struct {
	// RelayURL is the https URL the browser dials over WebTransport, e.g.
	// https://example.com:4433. It is built from the configured public host
	// (not the relay's bind address) so a deployment behind a reverse proxy or
	// on a public interface advertises the host browsers actually use.
	RelayURL string `json:"relayUrl"`
	// CertHash is the lower-case hex SHA-256 of the relay's WebTransport cert,
	// pinned by the browser via serverCertificateHashes. Omitted when empty so
	// the frontend can surface remediation guidance (mirrors VITE_CERT_HASH's
	// optionality in dev).
	CertHash string `json:"certHash,omitempty"`
}

// NewConfig builds the /config payload. relayURL must be the full https:// URL
// the browser should dial over WebTransport.
func NewConfig(relayURL, certHashHex string) Config {
	return Config{
		RelayURL: relayURL,
		CertHash: certHashHex,
	}
}
