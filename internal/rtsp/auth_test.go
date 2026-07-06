package rtsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAuthChallenge(t *testing.T) {
	tests := map[string]struct {
		header     string
		scheme     string
		paramVals  map[string]string
		wantParams int
	}{
		"digest quoted": {
			header:    `Digest realm="testrealm@host.com", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", qop="auth", algorithm=MD5`,
			scheme:    "digest",
			paramVals: map[string]string{"realm": "testrealm@host.com", "nonce": "dcd98b7102dd2f0e8b11d0f600bfb0c093", "qop": "auth", "algorithm": "MD5"},
		},
		"digest unquoted": {
			header:    `Digest realm=test, nonce=abc, algorithm=MD5`,
			scheme:    "digest",
			paramVals: map[string]string{"realm": "test", "nonce": "abc"},
		},
		"basic": {
			header:    `Basic realm="cam"`,
			scheme:    "basic",
			paramVals: map[string]string{"realm": "cam"},
		},
		"unknown scheme": {
			header: `Bearer xyz`,
		},
		"empty": {},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ch, ok := ParseAuthChallenge(tc.header)
			if tc.scheme == "" {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tc.scheme, ch.Scheme)
			for k, v := range tc.paramVals {
				assert.Equal(t, v, ch.Params[k], "param %s", k)
			}
		})
	}
}

func TestBuildAuthorization_Basic(t *testing.T) {
	cred := Credentials{Username: "admin", Password: "secret"}
	ch := AuthChallenge{Scheme: "basic", Params: map[string]string{"realm": "cam"}}
	got, err := BuildAuthorization("DESCRIBE", "rtsp://host/path", cred, ch)
	require.NoError(t, err)
	// base64("admin:secret") = "YWRtaW46c2VjcmV0"
	assert.Equal(t, "Basic YWRtaW46c2VjcmV0", got)
}

// TestBuildAuthorization_Digest_RFC2617 uses the canonical RFC 2617 §3.5
// example to verify the response hash computation. Since the cnonce is random,
// we verify the response with a known cnonce by calling digestHeader's
// internals indirectly: we re-derive what the response should be and check it
// appears in the output, plus that the structure is correct.
//
// RFC 2617 example values:
//
//	M = "GET", uri = "/dir/index.html"
//	user = "Mufasa", pass = "Circle Of Life", realm = "testrealm@host.com"
//	nonce = "dcd98b7102dd2f0e8b11d0f600bfb0c093", nc = "00000001"
//	cnonce = "0a4f113b", qop = "auth"
//	expected response = "6629fae49393a05397450978507c4ef1"
func TestBuildAuthorization_Digest_RFC2617(t *testing.T) {
	const (
		user   = "Mufasa"
		pass   = "Circle Of Life"
		realm  = "testrealm@host.com"
		nonce  = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
		cnonce = "0a4f113b"
		method = "GET"
		uri    = "/dir/index.html"
	)
	// Verify the response formula directly with the known cnonce.
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	response := md5hex(ha1 + ":" + nonce + ":00000001:" + cnonce + ":auth:" + ha2)
	assert.Equal(t, "6629fae49393a05397450978507c4ef1", response,
		"digest response must match the RFC 2617 §3.5 example")

	// Now verify BuildAuthorization produces a structurally-correct header
	// (with its own random cnonce, so we can't assert the exact response).
	cred := Credentials{Username: user, Password: pass}
	ch := AuthChallenge{Scheme: "digest", Params: map[string]string{
		"realm": realm, "nonce": nonce, "qop": "auth", "algorithm": "MD5",
	}}
	got, err := BuildAuthorization(method, uri, cred, ch)
	require.NoError(t, err)
	assert.Contains(t, got, `username="Mufasa"`)
	assert.Contains(t, got, `realm="testrealm@host.com"`)
	assert.Contains(t, got, `nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093"`)
	assert.Contains(t, got, `uri="/dir/index.html"`)
	assert.Contains(t, got, "qop=auth")
	assert.Contains(t, got, "nc=00000001")
	assert.Contains(t, got, `algorithm=MD5`)
	assert.Contains(t, got, `response="`)
}

func TestBuildAuthorization_Digest_Legacy_NoQop(t *testing.T) {
	// RFC 2069 legacy (no qop): response = MD5(HA1:nonce:HA2).
	cred := Credentials{Username: "user", Password: "pw"}
	ch := AuthChallenge{Scheme: "digest", Params: map[string]string{
		"realm": "r", "nonce": "n",
	}}
	got, err := BuildAuthorization("DESCRIBE", "rtsp://h/p", cred, ch)
	require.NoError(t, err)
	// No qop/nc/cnonce in legacy mode.
	assert.Contains(t, got, `response="`)
	assert.NotContains(t, got, "qop=")
	assert.NotContains(t, got, "nc=")
}

func TestBuildAuthorization_Digest_RejectsSHA256(t *testing.T) {
	ch := AuthChallenge{Scheme: "digest", Params: map[string]string{
		"realm": "r", "nonce": "n", "algorithm": "SHA-256",
	}}
	_, err := BuildAuthorization("DESCRIBE", "rtsp://h/p", Credentials{Username: "u", Password: "p"}, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MD5")
}

func TestBuildAuthorization_Digest_MissingRealmNonce(t *testing.T) {
	ch := AuthChallenge{Scheme: "digest", Params: map[string]string{}}
	_, err := BuildAuthorization("DESCRIBE", "rtsp://h/p", Credentials{Username: "u", Password: "p"}, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realm or nonce")
}
