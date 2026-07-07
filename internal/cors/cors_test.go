package cors

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewChecker(t *testing.T) {
	tests := map[string]struct {
		allowed []string
		origin  string // Origin header; "" = absent
		host    string // request Host
		want    bool
	}{
		"no origin header passes (non-browser)":     {host: "relay:4433", want: true},
		"same-origin passes by default":             {origin: "https://relay:4433", host: "relay:4433", want: true},
		"cross-origin rejected by default (secure)": {origin: "http://localhost:5178", host: "127.0.0.1:4433", want: false},
		"explicit allowed origin passes":            {allowed: []string{"http://localhost:5178"}, origin: "http://localhost:5178", host: "127.0.0.1:4433", want: true},
		"non-listed cross-origin rejected":          {allowed: []string{"http://localhost:5178"}, origin: "http://evil.example", host: "127.0.0.1:4433", want: false},
		"wildcard allows any origin":                {allowed: []string{"*"}, origin: "http://evil.example", host: "127.0.0.1:4433", want: true},
		"wildcard alongside explicit entries":       {allowed: []string{"https://app.example", "*"}, origin: "https://other.example", host: "127.0.0.1:4433", want: true},
		"host comparison is case-insensitive":       {origin: "https://RELAY:4433", host: "relay:4433", want: true},
		"same-origin differing port rejected":       {origin: "https://relay.example", host: "relay.example:4433", want: false},
		"malformed origin rejected":                 {allowed: []string{"http://localhost:5178"}, origin: "://bad", host: "127.0.0.1:4433", want: false},
		"same-host mode allows differing port":      {allowed: []string{"same-host"}, origin: "http://localhost:5178", host: "localhost:4433", want: true},
		"same-host mode allows portless origin":     {allowed: []string{"same-host"}, origin: "http://relay.example", host: "relay.example:4433", want: true},
		"same-host mode rejects different host":     {allowed: []string{"same-host"}, origin: "http://evil.example", host: "relay.example:4433", want: false},
		"same-host mode is case-insensitive":        {allowed: []string{"same-host"}, origin: "http://LOCALHOST:5178", host: "localhost:4433", want: true},
		"ipv6 same-origin":                          {allowed: []string{"http://dummy.example"}, origin: "https://[::1]:4433", host: "[::1]:4433", want: true},
		"ipv6 same-host differing port":             {allowed: []string{"same-host"}, origin: "http://[::1]:5178", host: "[::1]:4433", want: true},
		"domain suffix spoofing rejected":           {allowed: []string{"http://dummy.example"}, origin: "https://evil-relay.example:4433", host: "relay.example:4433", want: false},
		"subdomain spoofing rejected":               {allowed: []string{"http://dummy.example"}, origin: "https://sub.relay.example:4433", host: "relay.example:4433", want: false},
		"malformed request host rejected":           {allowed: []string{"http://dummy.example"}, origin: "http://localhost:5178", host: "127.0.0.1:4433:5555", want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodConnect, "/", nil)
			r.Host = tt.host
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			assert.Equal(t, tt.want, NewChecker(tt.allowed)(r))
		})
	}
}

func TestNewChecker_Logging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	oldLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldLogger)

	checker := NewChecker([]string{"http://dummy.example"})
	r := httptest.NewRequest(http.MethodConnect, "/", nil)
	r.Host = "127.0.0.1:4433"
	r.Header.Set("Origin", "http://evil.example")

	assert.False(t, checker(r))

	logOutput := buf.String()
	assert.True(t, strings.Contains(logOutput, "webtransport origin rejected"))
	assert.True(t, strings.Contains(logOutput, "origin=http://evil.example"))
	assert.True(t, strings.Contains(logOutput, "host=127.0.0.1:4433"))
}

func TestLoadAllowed(t *testing.T) {
	tests := map[string]struct {
		env  string
		want []string
	}{
		"empty returns nil (secure default)": {"", nil},
		"parses, trims, keeps wildcard": {
			" http://localhost:5178 , https://demo.qumo.dev ,* ",
			[]string{"http://localhost:5178", "https://demo.qumo.dev", "*"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvVar, tt.env)
			assert.Equal(t, tt.want, LoadAllowed())
		})
	}
}
