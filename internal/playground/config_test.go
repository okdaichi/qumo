package playground

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig_PassesThroughFields(t *testing.T) {
	cfg := NewConfig("https://example.com:4433", "abcd")
	assert.Equal(t, "https://example.com:4433", cfg.RelayURL)
	assert.Equal(t, "abcd", cfg.CertHash)
}

func TestConfig_CertHashOmittedWhenEmpty(t *testing.T) {
	// A built binary always serves a hash, but the dev fallback path (no /config)
	// means the frontend tolerates an absent hash; the JSON should omit it.
	cfg := NewConfig("https://localhost:4433", "")
	b, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"relayUrl":"https://localhost:4433"`)
	assert.NotContains(t, string(b), "certHash", "empty certHash must be omitted")
}
