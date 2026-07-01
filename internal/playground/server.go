package playground

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server serves the embedded playground web UI and the /config endpoint over
// plain HTTP on a loopback port. The page fetches /config same-origin, then
// dials the relay over WebTransport cross-origin (pinned by cert hash), so no
// CORS handling is needed here.
type Server struct {
	// relayPort is the port the in-process relay listens on. The /config
	// relayUrl is built per-request from the browser's own Host (the host the UI
	// was opened at) plus this port, so it always matches whatever address the
	// user — or their reverse proxy — is serving the UI under.
	relayPort string
	certHash  string
	assets    fs.FS
	addr      string
	httpSrv   *http.Server
}

// NewServer constructs a UI server ready to ListenAndServe. relayPort is the
// relay's listen port (used to build the per-request relayUrl); certHash is the
// pinned SHA-256 of the relay cert. assets must be the embedded dist filesystem
// already sub-rooted at its content root (files at the FS root, incl. index.html).
func NewServer(addr, relayPort, certHash string, assets fs.FS) *Server {
	s := &Server{relayPort: relayPort, certHash: certHash, assets: assets, addr: addr}

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

// handleAssets serves embedded static files, falling back to index.html for any
// path that isn't a real file. There is no client-side router today, but the
// fallback keeps deep links working and future-proofs SPA routing.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
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
