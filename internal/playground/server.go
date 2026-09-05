package playground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/qumo-dev/qumo/internal/cors"
	"github.com/qumo-dev/qumo/internal/ingest"
)

// Server serves the embedded playground web UI and the /config endpoint over
// plain HTTP on a loopback port. The page fetches /config same-origin, then
// dials the relay over WebTransport cross-origin (pinned by cert hash), so no
// CORS handling is needed here.
type Server struct {
	relayPort string
	certHash  string
	certFile  string
	keyFile   string
	assets    fs.FS
	addr      string
	httpSrv   *http.Server

	// assetsErr records that the embedded dist tree is the committed
	// placeholder (bundles never built — see VerifyAssets). While set, the UI
	// routes serve an explanatory error page instead of the broken index.html.
	assetsErr error

	// RTSP pull state (playground-only, in-process).
	pullMu     sync.Mutex
	pullHandle pullHandle
	pullCtx    context.Context
	pullCancel context.CancelFunc

	// pullStarter starts an RTSP pull ingest. Defaults to ingest.PullAndServe;
	// overridable in tests so the HTTP handlers can be exercised without binding
	// a real QUIC listener or presenting a certificate.
	pullStarter func(ctx context.Context, cfg ingest.PullConfig) (pullHandle, error)
}

// pullHandle is the subset of *ingest.PullHandle the playground server uses. It
// exists as an interface so tests can substitute a fake without standing up a
// real RTSP pull + MoQT server.
type pullHandle interface {
	SourceURL() string
	Path() string
	LastErr() string
	Close()
	Wait()
}

// NewServerWithCerts is like NewServer but also passes the cert/key file paths
// needed for the /api/pull endpoint (which starts an in-process RTSP pull
// client that serves MoQT on :4543 with the same cert).
func NewServerWithCerts(addr, relayPort, certHash, certFile, keyFile string, assets fs.FS) *Server {
	s := &Server{
		relayPort: relayPort,
		certHash:  certHash,
		certFile:  certFile,
		keyFile:   keyFile,
		assets:    assets,
		assetsErr: VerifyAssets(assets),
		addr:      addr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/api/pull", s.handlePullStart)
	mux.HandleFunc("/api/pull/stop", s.handlePullStop)
	mux.HandleFunc("/api/pull/status", s.handlePullStatus)
	mux.HandleFunc("/", s.handleAssets)

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// NewServer constructs a UI server ready to ListenAndServe. relayPort is the
// relay's listen port (used to build the per-request relayUrl); certHash is the
// pinned SHA-256 of the relay cert. assets must be the embedded dist filesystem
// already sub-rooted at its content root (files at the FS root, incl. index.html).
func NewServer(addr, relayPort, certHash string, assets fs.FS) *Server {
	s := &Server{relayPort: relayPort, certHash: certHash, assets: assets, assetsErr: VerifyAssets(assets), addr: addr}

	mux := http.NewServeMux()
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/", s.handleAssets)

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // matches the relay
	}
	return s
}

// ListenAndServe blocks until Shutdown is called or the server errors. It
// returns http.ErrServerClosed on a graceful shutdown.
func (s *Server) ListenAndServe() error {
	err := s.httpSrv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// URL returns the http:// URL the user should open in a browser.
func (s *Server) URL() string {
	return "http://" + s.addr
}

// handleConfig serves the runtime configuration consumed by the frontend. The
// relayUrl is derived from the request so it tracks whatever host the browser
// reached the UI through (localhost in dev, the public domain behind a proxy).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	relayURL := "https://" + net.JoinHostPort(requestHost(r), s.relayPort)
	cfg := NewConfig(relayURL, s.certHash)

	w.Header().Set("Content-Type", "application/json")
	// The cert hash reflects the current dev cert and must never be cached
	// across regenerations, so suppress all client-side storage of /config.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cfg)
}

// requestHost returns the hostname the browser used to reach the UI. It prefers
// X-Forwarded-Host (set by reverse proxies) and falls back to the request's
// Host header, stripping any port since the relay port is supplied separately.
func requestHost(r *http.Request) string {
	h := r.Header.Get("X-Forwarded-Host")
	if h == "" {
		h = r.Host
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return h // no port present
	}
	return host
}

// --- RTSP pull API (playground-only) ---

type pullRequest struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

type pullStatusResponse struct {
	Active bool   `json:"active"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	Error  string `json:"error,omitempty"`
}

// validPullURL validates a user-supplied RTSP source URL before the server
// dials it. It requires an rtsp:// or rtspd:// scheme with a non-empty host —
// defense-in-depth against SSRF (the RTSP dialer only speaks RTSP, but reject
// non-RTSP schemes early and explicitly). It deliberately does NOT block
// private/LAN hosts: pulling from an IP camera on the local network is the
// feature's primary use case; the playground is a local dev tool and must not
// be publicly exposed with /api/pull reachable (see the public-hosting note in
// the README).
func validPullURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "rtsp" && u.Scheme != "rtspd" {
		return errors.New("url must be rtsp:// or rtspd://")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	return nil
}

// validPullPath validates the user-supplied broadcast path. moqt only requires
// it start with '/', so a path containing spaces, control characters, or shell
// metacharacters would be accepted as a routing key and written verbatim into
// logs — a log-injection / routing-confusion surface. Restrict to a URL-safe
// charset and a sane length bound.
func validPullPath(path string) error {
	if path == "" || path[0] != '/' {
		return errors.New("path must start with '/'")
	}
	if len(path) > 128 {
		return errors.New("path too long (max 128 characters)")
	}
	for _, r := range path {
		if !isPathSafeRune(r) {
			return errors.New("path may only contain letters, digits, '/', '-', '_', '.', '~'")
		}
	}
	return nil
}

// isPathSafeRune reports whether r is an unreserved URL-path character: ASCII
// letters, digits, and "/-_.~".
func isPathSafeRune(r rune) bool {
	switch r {
	case '/', '-', '_', '.', '~':
		return true
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func (s *Server) handlePullStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.pullMu.Lock()
	if s.pullHandle != nil {
		s.pullMu.Unlock()
		http.Error(w, "pull already active — stop it first", http.StatusConflict)
		return
	}
	s.pullMu.Unlock()

	var req pullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	if err := validPullURL(req.URL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := req.Path
	if path == "" {
		path = "/live/camera"
	}
	if err := validPullPath(path); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	starter := s.pullStarter
	if starter == nil {
		// Production default. Pulled into a closure so a test can substitute
		// s.pullStarter without standing up a real QUIC listener + cert.
		starter = func(ctx context.Context, cfg ingest.PullConfig) (pullHandle, error) {
			return ingest.PullAndServe(ctx, cfg)
		}
	}
	handle, err := starter(ctx, ingest.PullConfig{
		SourceURL:     req.URL,
		BroadcastPath: path,
		ServeAddr:     ":4543",
		CertFile:      s.certFile,
		KeyFile:       s.keyFile,
		// The pull's MoQT server is a localhost dev tool started on-demand from
		// the UI. SameHost allows the browser's WebTransport handshake despite a
		// port mismatch between the UI origin and the :4543 pull server
		// (equalHost compares hostnames case-insensitively, ignoring port), while
		// still rejecting cross-origin pages. Note this does NOT bridge distinct
		// hostnames: access via 127.0.0.1 vs localhost will be rejected, so the
		// UI must be reached on the same hostname as VITE_RELAY_URL.
		AllowedOrigins: []string{cors.SameHost},
	})
	if err != nil {
		cancel()
		slog.Error("RTSP pull start failed", "error", err)
		http.Error(w, "pull start failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.pullMu.Lock()
	s.pullHandle = handle
	s.pullCtx = ctx
	s.pullCancel = cancel
	s.pullMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(pullStatusResponse{
		Active: true,
		URL:    handle.SourceURL(),
		Path:   handle.Path(),
	})
}

func (s *Server) handlePullStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.pullMu.Lock()
	handle := s.pullHandle
	s.pullHandle = nil
	s.pullCancel = nil
	s.pullCtx = nil
	s.pullMu.Unlock()

	if handle == nil {
		http.Error(w, "no active pull", http.StatusNotFound)
		return
	}

	handle.Close() // also calls h.cancel() — no need to cancel again

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(pullStatusResponse{Active: false})
}

func (s *Server) handlePullStatus(w http.ResponseWriter, r *http.Request) {
	s.pullMu.Lock()
	handle := s.pullHandle
	s.pullMu.Unlock()

	resp := pullStatusResponse{Active: false}
	if handle != nil {
		resp.Active = true
		resp.URL = handle.SourceURL()
		resp.Path = handle.Path()
		resp.Error = handle.LastErr()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleAssets serves embedded static files, falling back to index.html for any
// path that isn't a real file. There is no client-side router today, but the
// fallback keeps deep links working and future-proofs SPA routing.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	// Placeholder dist (bundles never built): serve an explanatory page instead
	// of index.html, whose hashed asset references would white-screen the tab.
	if s.assetsErr != nil {
		s.serveAssetsError(w)
		return
	}
	// http.FileServer already serves index.html for "/" and existing files; we
	// only need to synthesize the SPA fallback for non-existent paths.
	if r.URL.Path == "/" {
		http.FileServerFS(s.assets).ServeHTTP(w, r)
		return
	}
	// fs.Stat paths are relative to the FS root (no leading slash); r.URL.Path
	// always begins with "/", so trim it before checking whether a real file
	// exists.
	if _, err := fs.Stat(s.assets, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
		http.FileServerFS(s.assets).ServeHTTP(w, r)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	http.FileServerFS(s.assets).ServeHTTP(w, r2)
}

// serveAssetsError writes the in-browser explanation for a binary built
// without the Vite bundles (#376): without it the browser shows a blank page
// and a module MIME-type console error with no hint at the cause. The relay
// and /config keep working, so this is a 500 page, not a shutdown.
func (s *Server) serveAssetsError(w http.ResponseWriter) {
	const page = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>qumo playground — web UI not bundled</title>
<style>
body{font:16px/1.6 system-ui,sans-serif;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#222}
code{background:#f3f3f3;padding:.1rem .4rem;border-radius:4px;font-size:.9em}
pre{background:#f3f3f3;padding:1rem;border-radius:8px;overflow-x:auto}
h1{font-size:1.3rem}
</style>
</head>
<body>
<h1>qumo playground — web UI not bundled</h1>
<p>This qumo binary was built without the playground web UI, so there is
nothing to serve here. The relay itself is running; only the browser interface
is missing.</p>
<p><code>%s</code></p>
<p>To get the web UI, rebuild from a source checkout:</p>
<pre>git clone https://github.com/qumo-dev/qumo
cd qumo
%s
go build
./qumo playground</pre>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	// The two %s render the same VerifyAssets result logged at startup
	// (missing-file detail + rebuild hint), so the terminal and the browser
	// agree on what's missing and how to fix it.
	// not actionable: a failed write to the client connection can't be
	// recovered — the handler has nothing else to report.
	_, _ = fmt.Fprintf(w, page, html.EscapeString(s.assetsErr.Error()), html.EscapeString(buildAssetsHint))
}
