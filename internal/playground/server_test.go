package playground

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAssets builds an in-memory FS mimicking the embedded dist layout:
// index.html at the root plus a nested asset.
func newTestAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>qumo</title>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
}

func TestServer_ConfigDerivesRelayURLFromHost(t *testing.T) {
	// The relayUrl must follow whatever host the browser opened the UI at —
	// localhost in dev, the public domain behind a proxy. The relay port comes
	// from the server config.
	srv := NewServer("127.0.0.1:0", "4433", "deadbeef", newTestAssets())

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	var cfg Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cfg))
	assert.Equal(t, "https://localhost:4433", cfg.RelayURL)
	assert.Equal(t, "deadbeef", cfg.CertHash)
}

func TestServer_ConfigHonorsForwardedHost(t *testing.T) {
	// Behind a reverse proxy, X-Forwarded-Host carries the public origin; the
	// UI's own Host (the proxy's internal address) must not leak into relayUrl.
	srv := NewServer("127.0.0.1:0", "4433", "deadbeef", newTestAssets())

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-Host", "example.com")
	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, req)

	var cfg Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cfg))
	assert.Equal(t, "https://example.com:4433", cfg.RelayURL)
}

func TestServer_ConfigUsesCustomRelayPort(t *testing.T) {
	srv := NewServer("127.0.0.1:0", "8443", "deadbeef", newTestAssets())

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, req)

	var cfg Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cfg))
	assert.Equal(t, "https://example.com:8443", cfg.RelayURL)
}

func TestServer_ServesIndex(t *testing.T) {
	srv := NewServer("127.0.0.1:0", "4433", "", newTestAssets())

	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<title>qumo</title>")
}

func TestServer_ServesNestedAsset(t *testing.T) {
	srv := NewServer("127.0.0.1:0", "4433", "", newTestAssets())

	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	assert.Equal(t, "console.log('app')", string(body))
}

func TestServer_SPAFallbackForUnknownPath(t *testing.T) {
	srv := NewServer("127.0.0.1:0", "4433", "", newTestAssets())

	// A path with no matching file should fall back to index.html (SPA-style),
	// not 404.
	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/deep/route", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<title>qumo</title>")
}
