package playground

import (
	"os"
	"testing"

	"github.com/qumo-dev/qumo/internal/cors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIDisplayURL(t *testing.T) {
	tests := map[string]struct {
		uiAddr string
		want   string
	}{
		"loopback bind":           {"127.0.0.1:8080", "http://127.0.0.1:8080"},
		"wildcard to localhost":   {"0.0.0.0:8080", "http://localhost:8080"},
		"empty host to localhost": {":8080", "http://localhost:8080"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, uiDisplayURL(tt.uiAddr))
		})
	}
}

func TestConfigureRelayEnv_SetsAdvertiseAddr(t *testing.T) {
	// A wildcard relay bind must get an ADVERTISE_ADDR so the relay's
	// wildcard-address guard passes; with no public host known at startup, it
	// falls back to "localhost".
	t.Setenv("ADVERTISE_ADDR", "")
	t.Setenv("RELAY_NAME", "")

	cert, err := EnsureCert(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, configureRelayEnv("0.0.0.0:4433", cert))

	assert.Equal(t, "0.0.0.0:4433", os.Getenv("RELAY_ADDR"))
	assert.Equal(t, "localhost:4433", os.Getenv("ADVERTISE_ADDR"))
	assert.Equal(t, cert.CertFile, os.Getenv("CERT_FILE"))
	assert.Equal(t, cert.KeyFile, os.Getenv("KEY_FILE"))
	assert.Equal(t, "playground", os.Getenv("RELAY_NAME"))
}

func TestConfigureRelayEnv_LoopbackAdvertisesBindHost(t *testing.T) {
	cert, err := EnsureCert(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, configureRelayEnv("127.0.0.1:4433", cert))
	assert.Equal(t, "127.0.0.1:4433", os.Getenv("ADVERTISE_ADDR"))
}

func TestConfigureRelayEnv_DoesNotStompRelayName(t *testing.T) {
	t.Setenv("RELAY_NAME", "my-relay")
	cert, err := EnsureCert(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, configureRelayEnv("127.0.0.1:4433", cert))
	assert.Equal(t, "my-relay", os.Getenv("RELAY_NAME"))
}

func TestConfigureRelayEnv_DefaultsCORSAllowedOrigins(t *testing.T) {
	// The UI and relay share a host but differ by port; the same-host default
	// lets the browser connect out of the box without configuring CORS.
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	cert, err := EnsureCert(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, configureRelayEnv("127.0.0.1:4433", cert))
	assert.Equal(t, cors.SameHost, os.Getenv("CORS_ALLOWED_ORIGINS"))
}

func TestConfigureRelayEnv_RespectsExplicitCORSAllowedOrigins(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.com")
	cert, err := EnsureCert(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, configureRelayEnv("127.0.0.1:4433", cert))
	assert.Equal(t, "https://app.example.com", os.Getenv("CORS_ALLOWED_ORIGINS"))
}
