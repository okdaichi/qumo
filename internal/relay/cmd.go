package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/cors"
)

// sanitizeLog strips CR and LF from s to prevent log injection.
func sanitizeLog(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// Run starts the MoQ relay server.
//
// Configuration is read from environment variables:
//
//	RELAY_ADDR                   - listen address (default: ":4433", dual-stack:
//	                               binds both IPv4 and IPv6 so `localhost` works
//	                               on hosts where it resolves to ::1)
//	CERT_FILE                    - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE                     - TLS key file (default: "certs/server.key")
//	CA_FILE                      - PEM CA certificate; enables mTLS when set
//	MTLS_REQUIRED                - "true" to require a client cert on every connection
//	                               (default: false)
//	RELAY_NAME                   - node ID (default: "relay-" + hostname)
//	REGION                       - geographic region (default: "")
//	ROLE                         - node role: "hub" or "edge" (default: "")
//	ADVERTISE_ADDR               - address advertised to peers
//	GROUP_CACHE_SIZE             - max group caches (default: 100) [TODO]
//	FRAME_CAPACITY               - frame buffer size in bytes (default: 1500) [TODO]
//	PEERS                        - comma-separated list of static peer addresses	//	LOCAL_RESOLVER_ADDR            - Nomad HTTP API address (default: http://localhost:4646)
//	LOCAL_RESOLVER_SERVICE_NAME    - Nomad service name to query (default: "qumo-relay")
//	LOCAL_RESOLVER_INTERVAL        - Nomad discovery polling interval (default: "15s")
//	REMOTE_RESOLVER_URL      - remote traffic resolver URL (optional)
//	REMOTE_AUTH_TOKEN        - bearer token for remote resolver
//	REMOTE_RESOLVE_INTERVAL  - remote discovery polling interval (default: "15s")
//	REMOTE_TLS_ENABLED       - "true" to enable TLS for remote resolver
//	CORS_ALLOWED_ORIGINS     - comma-separated WebTransport origins allowed to
//	                           connect (default: same-origin only; "*" allows any;
//	                           "same-host" allows any port on the request's host).
//	                           Set this when serving the UI from a different
//	                           origin than the relay (e.g. a Vite dev server).
func Run(args []string) error {
	// Execution modes are flags (discoverable, self-documenting); secrets and
	// deployment configuration stay env vars (see relay-config.example.env).
	flags, err := parseRelayArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			relayUsage(os.Stdout)
			return nil
		}
		return err
	}

	addr := envOr("RELAY_ADDR", ":4433")
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")

	hostname, _ := os.Hostname()
	nodeID := envOr("RELAY_NAME", "relay-"+hostname)

	groupCacheSize, err := envInt("GROUP_CACHE_SIZE", DefaultGroupCacheSize)
	if err != nil {
		return fmt.Errorf("invalid GROUP_CACHE_SIZE: %w", err)
	}
	frameCapacity, err := envInt("FRAME_CAPACITY", 1500)
	if err != nil {
		return fmt.Errorf("invalid FRAME_CAPACITY: %w", err)
	}

	advertiseAddr := os.Getenv("ADVERTISE_ADDR")
	if advertiseAddr == "" {
		if isWildcardAddress(addr) {
			return fmt.Errorf("ADVERTISE_ADDR is required when RELAY_ADDR is %q", addr)
		}
		advertiseAddr = addr
	}

	var peers []Peer
	if raw := os.Getenv("PEERS"); raw != "" {
		for p := range strings.SplitSeq(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, Peer{Address: p})
			}
		}
	}

	// Setup TLS before remote resolver TLS config is built.
	tlsConfig, err := setupTLS(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to setup TLS: %w", err)
	}

	// mTLS: load CA pool and configure mutual authentication when CA_FILE is set.
	// Default (MTLS_REQUIRED unset): VerifyClientCertIfGiven — relay peers are verified,
	// browser clients without a cert are still allowed through (Nginx "optional" mode).
	// MTLS_REQUIRED=true: RequireAndVerifyClientCert — every connection must present a
	// cert signed by the CA (use this for relay-only clusters with no browser traffic).
	caPool, err := loadCACertPool(os.Getenv("CA_FILE"))
	if err != nil {
		return fmt.Errorf("failed to load CA_FILE: %w", err)
	}
	if caPool != nil {
		clientAuth := tls.VerifyClientCertIfGiven
		if os.Getenv("MTLS_REQUIRED") == "true" {
			clientAuth = tls.RequireAndVerifyClientCert
		}
		tlsConfig.ClientAuth = clientAuth
		tlsConfig.ClientCAs = caPool
		slog.Info("mTLS enabled on relay server",
			"ca_file", os.Getenv("CA_FILE"),
			"strict", clientAuth == tls.RequireAndVerifyClientCert,
		)
	}

	// Remote resolver TLS: present this node's cert and trust only the CA pool.
	// Built only when mTLS is active (caPool != nil).
	var remoteResolverTLS *tls.Config
	if caPool != nil {
		remoteResolverTLS = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: tlsConfig.Certificates, // relay cert used as client cert
			RootCAs:      caPool,
		}
	}

	// Create peer resolvers.
	localResolver := NewLocalResolver()
	remoteResolver := NewRemoteResolver(remoteResolverTLS)

	// Credential client: credential introspection + usage metering (optional).
	credentialClient := NewCredentialClient()
	var meter *Meter
	if credentialClient != nil {
		meter = newMeter(credentialClient)
	}

	relayCfg := Config{
		NodeID:                nodeID,
		Role:                  flags.Role,
		AdvertiseAddr:         advertiseAddr,
		GroupCacheSize:        groupCacheSize,
		FrameCapacity:         frameCapacity,
		Peers:                 peers,
		LocalResolverInterval: localResolver.Interval(),
		NextSessionURI:        os.Getenv("GOAWAY_REDIRECT_URI"),
	}
	// The remote resolver is optional: NewRemoteResolver returns nil when
	// REMOTE_RESOLVER_URL is unset (the common single-node/demo case). The
	// consumer already treats a nil resolver + zero interval as "disabled"
	// (server.go), so mirror that here rather than dereferencing nil.
	if remoteResolver != nil {
		relayCfg.RemoteResolverInterval = remoteResolver.Interval()
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create relay server
	httpMux := http.NewServeMux()

	quicConfig := &quic.Config{
		Allow0RTT:                        true,
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		KeepAlivePeriod:                  10 * time.Second,
		MaxIdleTimeout:                   60 * time.Second,
	}

	// Dialer TLS: advertise only moqt ALPN for native QUIC peer connections.
	// The server TLS config advertises ["h3", "moqt"] to support both
	// WebTransport (browsers) and native QUIC (peer relays). If the dialer
	// sends both, TLS ALPN picks "h3" first → QPACK decompression failure.
	dialerTLS := tlsConfig.Clone()
	dialerTLS.NextProtos = []string{moqt.NextProtoMOQ}
	// Carry over mTLS settings: trust only the CA pool and strip client-auth fields
	// (ClientAuth/ClientCAs are server-side settings; the dialer uses RootCAs).
	dialerTLS.ClientAuth = tls.NoClientCert
	dialerTLS.ClientCAs = nil
	if caPool != nil {
		dialerTLS.RootCAs = caPool
	}

	trackMux := moqt.NewTrackMux(moqt.NewHopID())
	relayServer := &Server{
		MOQServer: &moqt.Server{
			Addr:               addr,
			TLSConfig:          tlsConfig,
			QUICConfig:         quicConfig,
			WebTransportServer: moqt.NewWebTransportServer(httpMux),
			NextSessionURI:     relayCfg.NextSessionURI,
		},
		MOQDialer: &moqt.Dialer{
			TLSConfig:  dialerTLS,
			QUICConfig: quicConfig,
			OnGoaway:   handlePeerGoaway,
		},
		Config:           &relayCfg,
		TrackMux:         trackMux,
		AllowedOrigins:   cors.LoadAllowed(),
		localResolver:    localResolver,
		remoteResolver:   remoteResolver,
		credentialClient: credentialClient,
		meter:            meter,
	}

	httpMux.HandleFunc("/", relayServer.HandleWebTransport)
	httpMux.HandleFunc("/health", relayServer.ServeHealth)
	httpMux.Handle("/metrics", promhttp.Handler())

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("\t%-8s: %s\n", "Host", sanitizeLog(addr))
	log.Printf("\t%-8s: %s\n", "Advertise", sanitizeLog(relayCfg.AdvertiseAddr))
	log.Printf("\t%-8s: %s\n", "Node ID", sanitizeLog(relayCfg.NodeID))
	log.Printf("\t%-8s: WebTransport endpoint\n", "/")
	log.Printf("\t%-8s: health probe\n", "/health")
	log.Printf("\t%-8s: Prometheus metrics\n", "/metrics")
	for _, p := range relayCfg.Peers {
		log.Printf("\t%-8s: %s\n", "Peer", sanitizeLog(p.Address))
	}
	if remoteResolver != nil {
		log.Printf("\t%-8s: %s (interval: %s)\n", "Resolver", sanitizeLog(remoteResolver.url), remoteResolver.Interval())
	}
	if relayCfg.LocalResolverInterval > 0 {
		log.Printf("\t%-8s: %s (interval: %s)\n", "Resolver", "local ("+localResolver.serviceName+")", localResolver.Interval())
	}
	if credentialClient != nil {
		log.Printf("\t%-8s: %s (metering every 30s)\n", "Credentials", sanitizeLog(credentialClient.baseURL))
	}

	// Start peer connections in background
	go relayServer.ConnectPeers(ctx)

	// Start usage meter if credential features are active.
	if meter != nil {
		go meter.Run(ctx)
	}

	// Delegate to testable helper that runs servers until ctx is cancelled
	if err := serveComponents(ctx, relayServer, httpServer, 10*time.Second); err != nil {
		slog.Error("serveComponents failed", "err", err)
		cancel()
		return err
	}

	return nil
}

// server is a minimal interface implemented by both *Server and
// *http.Server so we can unit-test the run/shutdown flow with fakes.
type server interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// serveComponents starts the provided servers and blocks until ctx is cancelled.
// It recovers panics from ListenAndServe goroutines, returns the first
// observed error, and performs a graceful shutdown of both servers.
//
// Design notes:
//   - serveComponents owns panic recovery and error reporting but does *not*
//     call the caller's cancel; the caller decides how to handle returned
//     errors (and may cancel the parent context).
//   - We use explicit Shutdown() calls because ListenAndServe blocks until the
//     server stops (it does not return on context cancellation by itself).
//   - This function intentionally keeps explicit control flow rather than
//     using errgroup so the shutdown ordering is clear and testable.
func serveComponents(ctx context.Context, relaySrv server, httpSrv server, shutdownTimeout time.Duration) error {
	// Create a derived cancellable context we can cancel when servers exit.
	derivedCtx, derivedCancel := context.WithCancel(ctx)
	defer derivedCancel()

	g, gctx := errgroup.WithContext(derivedCtx)

	g.Go(func() (retErr error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in relay ListenAndServe", "panic", r)
				derivedCancel()
				retErr = fmt.Errorf("panic in relay ListenAndServe: %v", r)
			}
		}()

		if err := relaySrv.ListenAndServe(); err != nil {
			derivedCancel()
			return fmt.Errorf("relay ListenAndServe: %w", err)
		}
		derivedCancel()
		return nil
	})

	g.Go(func() (retErr error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in HTTP ListenAndServe", "panic", r)
				derivedCancel()
				retErr = fmt.Errorf("panic in HTTP ListenAndServe: %v", r)
			}
		}()

		if err := httpSrv.ListenAndServe(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				derivedCancel()
				return nil
			}
			derivedCancel()
			return fmt.Errorf("http ListenAndServe: %w", err)
		}
		derivedCancel()
		return nil
	})

	// Supervisor: when derived context is done, perform graceful shutdown.
	shutdownDone := make(chan struct{})
	go func() {
		<-gctx.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(gctx), shutdownTimeout)
		defer shutdownCancel()

		if err := relaySrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("relay shutdown error", "err", err)
		}
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP server shutdown error", "err", err)
		}

		close(shutdownDone)
	}()

	// Wait for goroutines to finish; err will be first non-nil error (if any).
	err := g.Wait()

	// Ensure shutdown completed before returning.
	<-shutdownDone

	return err
}

func isWildcardAddress(addr string) bool {
	return strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "0.0.0.0") || strings.HasPrefix(addr, "[::]") || addr == "::"
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envInt(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// loadCACertPool reads a PEM-encoded CA certificate file into an x509.CertPool.
// Returns (nil, nil) when caFile is empty — callers treat nil as "mTLS disabled".
// CA_FILE must be a relative path with no path traversal components.
func loadCACertPool(caFile string) (*x509.CertPool, error) {
	if caFile == "" {
		return nil, nil
	}
	if filepath.IsAbs(caFile) {
		return nil, fmt.Errorf("CA_FILE must be a relative path")
	}
	caFile = filepath.Clean(caFile)
	if caFile == ".." || strings.HasPrefix(caFile, ".."+string(filepath.Separator)) || strings.Contains(caFile, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("CA_FILE must not contain path traversal")
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file %q: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("no valid certificates in CA file %q", caFile)
	}
	return pool, nil
}

func setupTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificates: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3", moqt.NextProtoMOQ}, // HTTP/3 for WebTransport, MOQ native QUIC
	}, nil
}

// relayUsage writes the `qumo relay` flag summary to w. Flags cover execution
// modes only; everything else is configured via environment variables (see
// relay-config.example.env) to keep secrets out of argv and stay 12-factor
// friendly for container deployment.
func relayUsage(w io.Writer) {
	const help = `Usage: qumo relay [flags]

Start the MoQT relay server.

Flags:
  --role <hub|edge>  node topology role (default: flat / single-node)

All other configuration is via environment variables;
see relay-config.example.env for the full list.
`
	_, _ = io.WriteString(w, help)
}

// relayFlags holds parsed `qumo relay` execution-mode flags. Execution modes
// are flags; secrets and deployment configuration stay env (see
// relay-config.example.env). Add future runtime knobs (e.g. --log-level) here.
type relayFlags struct {
	// Role is the node's topology role: "hub" (inter-region), "edge"
	// (client-facing), or empty for a flat / single-node relay. Flag-only (no
	// env equivalent) to avoid two sources of truth.
	Role string
}

// parseRelayArgs parses `qumo relay` flags into a relayFlags. flag.ErrHelp is
// returned as-is so Run can render usage and exit 0.
func parseRelayArgs(args []string) (relayFlags, error) {
	var f relayFlags
	fs := flag.NewFlagSet("qumo relay", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Run owns help/usage rendering
	fs.StringVar(&f.Role, "role", "",
		`node topology role: "hub" (inter-region) or "edge" (client-facing); `+
			`empty (default) is a flat / single-node relay.`)
	if err := fs.Parse(args); err != nil {
		return relayFlags{}, err
	}
	if fs.NArg() > 0 {
		return relayFlags{}, fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	return f, nil
}
