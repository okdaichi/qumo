// Package playground powers the `qumo playground` subcommand: a self-contained
// local demo that starts the relay in-process, serves the embedded web UI, and
// exposes runtime configuration to the browser via a /config endpoint.
package playground

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// certValidity is the lifetime of generated dev certificates. Chrome's
// WebTransport serverCertificateHashes rejects self-signed certs valid for more
// than 14 days, so this must stay at or below that.
const certValidity = 14 * 24 * time.Hour

// certReuseThreshold is the minimum remaining validity before a cached cert is
// regenerated. Kept well below certValidity so a user never boots a cert that a
// browser will reject moments later, while still reusing across day-to-day runs.
const certReuseThreshold = 48 * time.Hour

// Cert is a dev WebTransport certificate ready to hand to the relay (via
// CERT_FILE/KEY_FILE) and to pin in the browser (via its SHA-256 hash).
type Cert struct {
	// CertFile / KeyFile are absolute paths to the PEM cert and key on disk.
	// The relay loads these via tls.LoadX509KeyPair, so they must exist as files.
	CertFile string
	KeyFile  string
	// HashHex is the lower-case hex SHA-256 of the certificate's DER bytes — the
	// value the browser pins in WebTransport's serverCertificateHashes.
	HashHex string
}

// EnsureCert returns a usable dev cert, generating one only when the cached copy
// is missing or within certReuseThreshold of expiry.
//
// dir selects the on-disk location: "" uses the per-user cache directory
// (os.UserCacheDir/qumo/playground), an explicit dir overrides it (useful for
// tests and for sharing a cert with `mage cert` via QUMO_PLAYGROUND_CERT_DIR).
func EnsureCert(dir string) (*Cert, error) {
	dir, err := resolveCertDir(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create cert dir %q: %w", dir, err)
	}

	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	c, err := loadCertIfFresh(certFile, keyFile)
	switch {
	case err == nil:
		return c, nil
	case errors.Is(err, errCertMissing), errors.Is(err, errCertStale):
		// Expected: regenerate below.
	default:
		// A parse/read failure is distinct from "missing/stale": surface it rather
		// than silently regenerating, since it may indicate a corrupt or
		// hand-edited file the user should know about.
		return nil, fmt.Errorf("read cached cert: %w", err)
	}

	return generateCert(certFile, keyFile)
}

// resolveCertDir resolves the cert directory, honoring an explicit Options
// value, then the QUMO_PLAYGROUND_CERT_DIR env var, then the per-user cache.
func resolveCertDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if env := os.Getenv("QUMO_PLAYGROUND_CERT_DIR"); env != "" {
		return env, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf(
			"could not determine per-user cache dir for dev cert: %w "+
				"(set QUMO_PLAYGROUND_CERT_DIR to choose a location)", err,
		)
	}
	return filepath.Join(cache, "qumo", "playground"), nil
}

// Sentinel errors for the cached-cert freshness check.
var (
	errCertMissing = errors.New("cert or key file missing")
	errCertStale   = errors.New("cached cert is near or past expiry")
)

// loadCertIfFresh returns the cached cert if both files exist and the cert has
// more than certReuseThreshold of validity remaining. Otherwise it returns a
// sentinel error describing why the cert can't be reused.
func loadCertIfFresh(certFile, keyFile string) (*Cert, error) {
	der, err := readCertDER(certFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errCertMissing
		}
		// File exists but is unreadable/unparseable — surface rather than
		// silently regenerating, since it may indicate corruption.
		return nil, err
	}
	if _, err := os.Stat(keyFile); err != nil {
		return nil, errCertMissing
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse cached cert: %w", err)
	}
	// Chrome's WebTransport serverCertificateHashes rejects any cert whose
	// validity period exceeds 14 days, so a long-lived cert (e.g. one signed by
	// mkcert when QUMO_PLAYGROUND_CERT_DIR points at `certs/`) can't be pinned.
	// Surface this rather than serving a hash the browser will reject — and rather
	// than regenerating, which would clobber the shared file. The fix is on the
	// user side (unset QUMO_PLAYGROUND_CERT_DIR to let playground mint its own
	// short-lived cert, or point it at a ≤14d cert).
	if validity := cert.NotAfter.Sub(cert.NotBefore); validity > certValidity {
		return nil, fmt.Errorf(
			"cached cert validity %s exceeds the WebTransport serverCertificateHashes %s limit; "+
				"unset QUMO_PLAYGROUND_CERT_DIR (or use a ≤%s cert) so playground can pin it",
			validity, certValidity, certValidity,
		)
	}
	if time.Until(cert.NotAfter) <= certReuseThreshold {
		return nil, errCertStale
	}

	c := &Cert{CertFile: certFile, KeyFile: keyFile, HashHex: sha256HexOfDER(der)}
	return c, nil
}

// generateCert writes a fresh ECDSA P-256 self-signed cert (valid for
// certValidity) and its key to the given paths and returns the resulting Cert.
func generateCert(certFile, keyFile string) (*Cert, error) {
	return generateCertValidFor(certFile, keyFile, certValidity)
}

// generateCertValidFor is generateCert with an explicit validity, used by tests
// to mint a near-expired cert (and by generateCert with certValidity).
func generateCertValidFor(certFile, keyFile string, validity time.Duration) (*Cert, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	notBefore := time.Now()
	notAfter := notBefore.Add(validity)

	// Mirrors the dev cert minted by `mage cert`: localhost + loopback IPs only,
	// since the playground relay is reached on 127.0.0.1 / localhost.
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"qumo dev"}},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}
	if err := writePEM(certFile, "CERTIFICATE", der); err != nil {
		return nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	if err := writePEM(keyFile, "EC PRIVATE KEY", keyDER); err != nil {
		return nil, err
	}

	return &Cert{CertFile: certFile, KeyFile: keyFile, HashHex: sha256HexOfDER(der)}, nil
}

// writePEM encodes a single PEM block to path with restrictive permissions.
func writePEM(path, blockType string, der []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

// readCertDER reads and PEM-decodes the certificate at path, returning its DER
// bytes. A failure to read or decode yields an error.
func readCertDER(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("failed to decode PEM")
	}
	return block.Bytes, nil
}

// sha256HexOfDER returns the lower-case hex SHA-256 of the certificate DER —
// the value WebTransport pins via serverCertificateHashes.
func sha256HexOfDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}
