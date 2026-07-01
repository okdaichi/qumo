package playground

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureCert_GeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()

	c1, err := EnsureCert(dir)
	require.NoError(t, err)
	require.NotNil(t, c1)
	assert.Equal(t, filepath.Join(dir, "server.crt"), c1.CertFile)
	assert.Equal(t, filepath.Join(dir, "server.key"), c1.KeyFile)
	assert.Len(t, c1.HashHex, 64, "hash must be 64 hex chars")

	// Files exist on disk.
	_, err = os.Stat(c1.CertFile)
	require.NoError(t, err)
	_, err = os.Stat(c1.KeyFile)
	require.NoError(t, err)

	// Hash matches an independent SHA-256 of the cert DER on disk.
	assertCertHashMatches(t, c1.CertFile, c1.HashHex)

	// Second call reuses the cached cert (same hash), no regeneration.
	c2, err := EnsureCert(dir)
	require.NoError(t, err)
	assert.Equal(t, c1.HashHex, c2.HashHex, "fresh cert should be reused")
}

func TestEnsureCert_RegeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()

	c1, err := EnsureCert(dir)
	require.NoError(t, err)

	require.NoError(t, os.Remove(c1.CertFile))

	c2, err := EnsureCert(dir)
	require.NoError(t, err)
	assert.NotEqual(t, c1.HashHex, c2.HashHex, "cert should regenerate after removal")
}

func TestEnsureCert_RegeneratesWhenStale(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	// Seed a cert whose validity is below the reuse threshold so EnsureCert
	// treats it as stale and regenerates with full validity.
	c1, err := generateCertValidFor(certFile, keyFile, 1*time.Hour)
	require.NoError(t, err)

	c2, err := EnsureCert(dir)
	require.NoError(t, err)
	assert.NotEqual(t, c1.HashHex, c2.HashHex, "stale cert should regenerate")

	// The regenerated cert must itself be reusable on the next call.
	c3, err := EnsureCert(dir)
	require.NoError(t, err)
	assert.Equal(t, c2.HashHex, c3.HashHex, "regenerated cert should be reused")
}

func TestEnsureCert_HonorsEnvOverride(t *testing.T) {
	// QUMO_PLAYGROUND_CERT_DIR overrides the per-user cache location.
	dir := t.TempDir()
	t.Setenv("QUMO_PLAYGROUND_CERT_DIR", dir)

	c, err := EnsureCert("")
	require.NoError(t, err)
	// The cert lands in the env-overridden dir, not the per-user cache.
	assert.Equal(t, filepath.Join(dir, "server.crt"), c.CertFile)
	assert.Equal(t, filepath.Join(dir, "server.key"), c.KeyFile)
}

func TestEnsureCert_RejectsCorruptCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	// Both files present, but the cert is not valid PEM/DER — EnsureCert must
	// surface this rather than silently regenerating.
	require.NoError(t, os.WriteFile(certPath, []byte("not a cert"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte("not a key"), 0o600))

	_, err := EnsureCert(dir)
	assert.Error(t, err)
}

func TestSha256HexOfDER(t *testing.T) {
	der := []byte{0x01, 0x02, 0x03}
	sum := sha256.Sum256(der)
	want := hex.EncodeToString(sum[:])
	assert.Equal(t, want, sha256HexOfDER(der))
}

// assertCertHashMatches independently computes the SHA-256 of the cert DER at
// path and asserts it equals wantHex.
func assertCertHashMatches(t *testing.T, path, wantHex string) {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(b)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	sum := sha256.Sum256(cert.Raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), wantHex)
}
