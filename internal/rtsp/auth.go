package rtsp

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Credentials are the username/password for RTSP authentication, extracted from
// the source URL (rtsp://user:pass@host/path).
type Credentials struct {
	Username string
	Password string
}

// HasCredentials reports whether both username and password are non-empty.
func (c Credentials) HasCredentials() bool {
	return c.Username != "" && c.Password != ""
}

// AuthChallenge is a parsed WWW-Authenticate header: the scheme (basic|digest)
// and its parameters (realm, nonce, qop, algorithm, opaque, …).
type AuthChallenge struct {
	Scheme string
	Params map[string]string
}

// ParseAuthChallenge parses a WWW-Authenticate (or Authorization) header value
// of the form `<scheme> <param=value>, <param=value>`. Values may be quoted
// ("...") or bare tokens. It returns ok=false if the header is empty or the
// scheme is unrecognised (only basic and digest are handled).
func ParseAuthChallenge(header string) (AuthChallenge, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return AuthChallenge{}, false
	}
	space := strings.IndexAny(header, " \t")
	scheme := strings.ToLower(header)
	rest := ""
	if space >= 0 {
		scheme = strings.ToLower(header[:space])
		rest = header[space+1:]
	}
	switch scheme {
	case "basic", "digest":
	default:
		return AuthChallenge{}, false
	}
	return AuthChallenge{Scheme: scheme, Params: parseAuthParams(rest)}, true
}

// parseAuthParams splits a comma-separated key=value list. Values may be
// quoted; commas inside quotes are preserved.
func parseAuthParams(s string) map[string]string {
	params := make(map[string]string)
	var key, val strings.Builder
	inQuotes := false
	inValue := false
	flush := func() {
		k := strings.TrimSpace(key.String())
		v := strings.TrimSpace(val.String())
		if k != "" {
			params[strings.ToLower(k)] = v
		}
		key.Reset()
		val.Reset()
		inValue = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '=':
			inValue = true
		case c == '"':
			inQuotes = !inQuotes
		case c == ',' && !inQuotes:
			flush()
		default:
			if inValue {
				val.WriteByte(c)
			} else {
				key.WriteByte(c)
			}
		}
	}
	flush()
	return params
}

// BuildAuthorization constructs the Authorization header value for the given
// RTSP method + URI, using the supplied credentials and server challenge. For
// Basic it always succeeds; for Digest it computes the MD5 response (RFC 2617
// qop=auth, or RFC 2069 legacy when no qop is advertised). auth-int and
// SHA-256 are not supported.
func BuildAuthorization(method, uri string, cred Credentials, ch AuthChallenge) (string, error) {
	switch ch.Scheme {
	case "basic":
		return "Basic " + basicHeader(cred), nil
	case "digest":
		return digestHeader(method, uri, cred, ch.Params)
	default:
		return "", fmt.Errorf("unsupported auth scheme %q", ch.Scheme)
	}
}

func basicHeader(cred Credentials) string {
	return base64.StdEncoding.EncodeToString([]byte(cred.Username + ":" + cred.Password))
}

func digestHeader(method, uri string, cred Credentials, p map[string]string) (string, error) {
	realm := p["realm"]
	nonce := p["nonce"]
	if realm == "" || nonce == "" {
		return "", fmt.Errorf("digest challenge missing realm or nonce")
	}
	algo := strings.ToUpper(p["algorithm"])
	if algo == "" {
		algo = "MD5"
	}
	if algo != "MD5" {
		return "", fmt.Errorf("digest algorithm %q not supported (only MD5)", algo)
	}

	ha1 := md5hex(fmt.Sprintf("%s:%s:%s", cred.Username, realm, cred.Password))
	ha2 := md5hex(fmt.Sprintf("%s:%s", method, uri))

	cnonce := randomHex(8)
	nc := "00000001"

	var response string
	parts := []string{
		fmt.Sprintf(`username="%s"`, cred.Username),
		fmt.Sprintf(`realm="%s"`, realm),
		fmt.Sprintf(`nonce="%s"`, nonce),
		fmt.Sprintf(`uri="%s"`, uri),
	}

	// qop may be absent (RFC 2069 legacy) or a list like "auth,auth-int".
	qop := selectQop(p["qop"])
	if qop != "" {
		response = md5hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
		parts = append(parts,
			fmt.Sprintf(`qop=%s`, qop),
			fmt.Sprintf(`nc=%s`, nc),
			fmt.Sprintf(`cnonce="%s"`, cnonce),
		)
	} else {
		response = md5hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	}
	parts = append(parts,
		fmt.Sprintf(`response="%s"`, response),
		fmt.Sprintf(`algorithm=%s`, algo),
	)
	if opaque := p["opaque"]; opaque != "" {
		parts = append(parts, fmt.Sprintf(`opaque="%s"`, opaque))
	}

	return "Digest " + strings.Join(parts, ", "), nil
}

// selectQop picks "auth" from a qop list, returning "" for legacy (no qop).
func selectQop(advertised string) string {
	if advertised == "" {
		return ""
	}
	for _, q := range strings.Split(advertised, ",") {
		if strings.TrimSpace(q) == "auth" {
			return "auth"
		}
	}
	// auth-int not supported; if only auth-int is offered, fall back to legacy.
	return ""
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
