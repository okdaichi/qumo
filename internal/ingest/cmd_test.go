package ingest

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvOr(t *testing.T) {
	key := "TEST_ENV_OR"
	defaultVal := "default"

	// Test default value
	os.Unsetenv(key)
	assert.Equal(t, defaultVal, envOr(key, defaultVal))

	// Test env value
	expected := "actual"
	t.Setenv(key, expected)
	assert.Equal(t, expected, envOr(key, defaultVal))
}

func TestNewOriginChecker(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		origin  string
		host    string
		want    bool
	}{
		{name: "headerless request allowed (non-browser)", allowed: nil, host: "relay.example.com", want: true},
		{name: "empty allowlist rejects cross-origin", allowed: nil, origin: "https://evil.example.com", host: "relay.example.com", want: false},
		{name: "empty allowlist allows same-origin", allowed: nil, origin: "https://relay.example.com", host: "relay.example.com", want: true},
		{name: "same-origin host is case-insensitive", allowed: nil, origin: "https://RELAY.Example.com", host: "relay.example.com", want: true},
		{name: "explicit allowlist match", allowed: []string{"https://app.example.com"}, origin: "https://app.example.com", host: "relay.example.com", want: true},
		{name: "allowlist mismatch rejected, not same-origin", allowed: []string{"https://app.example.com"}, origin: "https://evil.example.com", host: "relay.example.com", want: false},
		{name: "wildcard allows any origin", allowed: []string{"*"}, origin: "https://evil.example.com", host: "relay.example.com", want: true},
		{name: "wildcard alongside explicit entries", allowed: []string{"https://app.example.com", "*"}, origin: "https://other.example.com", want: true},
		{name: "unparseable origin rejected", allowed: []string{"https://app.example.com"}, origin: "://bad", host: "relay.example.com", want: false},
		{name: "same-origin with differing port rejected", allowed: nil, origin: "https://relay.example.com", host: "relay.example.com:4433", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{Header: http.Header{}, Host: tc.host}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			assert.Equal(t, tc.want, newOriginChecker(tc.allowed)(req))
		})
	}
}
