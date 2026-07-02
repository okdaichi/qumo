package cors

import (
	"net/http"
	"net/http/httptest"
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
