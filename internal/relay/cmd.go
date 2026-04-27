package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math/big"
	"net"
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
	"github.com/qumo-dev/qumo/internal/bootstrap"
)

// sanitizeLog strips CR and LF from s to prevent log injection.
func sanitizeLog(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// Run starts the MoQ relay server.
//
// Configuration is read from environment variables:
//
//	RELAY_ADDR          - listen address (default: "0.0.0.0:4433")
//	CERT_FILE           - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE            - TLS key file (default: "certs/server.key")
//	CA_FILE             - PEM CA certificate; enables mTLS when set:
//	                        relay server verifies peer certs (but allows clients without one),
//	                        dialer presents this node's cert to remote relays,
//	                        bootstrap HTTP clients use it as root CA and present client cert.
//	MTLS_REQUIRED       - "true" to require a client cert on every connection
//	                        (default: false — cert verified if presented, browsers still allowed)
//	RELAY_NAME          - node ID (default: "relay-" + hostname)
//	REGION              - geographic region (default: "")
//	ROLE                - node role: "hub" or "edge" (default: "")
//	ADVERTISE_ADDR      - address advertised to peers (required when RELAY_ADDR is wildcard)
//	GROUP_CACHE_SIZE    - max group caches (default: 100) [TODO: not yet wired to handler]
//	FRAME_CAPACITY      - frame buffer size in bytes (default: 1500) [TODO: not yet wired to handler]
//	PEERS               - comma-separated list of peer addresses
//	BOOTSTRAP_URLS      - comma-separated list of bootstrap server URLs
//	BOOTSTRAP_INTERVAL  - polling interval for bootstrap servers (default: "15s")
func Run(_ []string) error {
	addr := envOr("RELAY_ADDR", "0.0.0.0:4433")
	certFile := envOr("CERT_FILE", "certs/server.crt")
	keyFile := envOr("KEY_FILE", "certs/server.key")

	hostname, _ := os.Hostname()
	nodeID := envOr("RELAY_NAME", "relay-"+hostname)

	groupCacheSize, err := envInt("GROUP_CACHE_SIZE", 100)
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

	// Setup TLS before parsing bootstrap URLs so bootstrapClientTLS is in scope.
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

	// Bootstrap client TLS: present this node's cert and trust only the CA pool.
	// Built only when mTLS is active (caPool != nil).
	var bootstrapClientTLS *tls.Config
	if caPool != nil {
		bootstrapClientTLS = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: tlsConfig.Certificates, // relay cert used as client cert
			RootCAs:      caPool,
		}
	}

	var bootstraps []bootstrap.ClientConfig
	if raw := os.Getenv("BOOTSTRAP_URLS"); raw != "" {
		intervalStr := envOr("BOOTSTRAP_INTERVAL", "15s")
		interval, parseErr := time.ParseDuration(intervalStr)
		if parseErr != nil {
			return fmt.Errorf("invalid BOOTSTRAP_INTERVAL %q: %w", intervalStr, parseErr)
		}
		authToken := os.Getenv("BOOTSTRAP_AUTH_TOKEN")
		for u := range strings.SplitSeq(raw, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				bootstraps = append(bootstraps, bootstrap.ClientConfig{
					URL:       u,
					Interval:  interval,
					AuthToken: authToken,
					TLSConfig: bootstrapClientTLS,
				})
			}
		}
	}

	relayCfg := Config{
		NodeID:         nodeID,
		Region:         os.Getenv("REGION"),
		Role:           os.Getenv("ROLE"),
		AdvertiseAddr:  advertiseAddr,
		GroupCacheSize: groupCacheSize,
		FrameCapacity:  frameCapacity,
		Peers:          peers,
		Bootstraps:     bootstraps,
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
	if os.Getenv("INSECURE") == "true" {
		dialerTLS.InsecureSkipVerify = true //nolint:gosec // INSECURE mode only
	}

	trackMux := moqt.NewTrackMux(moqt.NewHopID())
	relayServer := &Server{
		MOQServer: &moqt.Server{
			Addr:               addr,
			TLSConfig:          tlsConfig,
			QUICConfig:         quicConfig,
			WebTransportServer: moqt.NewWebTransportServer(httpMux),
		},
		MOQDialer: &moqt.Dialer{
			TLSConfig:  dialerTLS,
			QUICConfig: quicConfig,
		},
		Config:   &relayCfg,
		TrackMux: trackMux,
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
	log.Printf("\t%-8s: %s\n", "Region", sanitizeLog(relayCfg.Region))
	log.Printf("\t%-8s: WebTransport endpoint\n", "/")
	log.Printf("\t%-8s: health probe\n", "/health")
	log.Printf("\t%-8s: Prometheus metrics\n", "/metrics")
	for _, p := range relayCfg.Peers {
		log.Printf("\t%-8s: %s\n", "Peer", sanitizeLog(p.Address))
	}
	for _, b := range relayCfg.Bootstraps {
		log.Printf("\t%-8s: %s (interval: %s)\n", "Bootstrap", sanitizeLog(b.URL), b.Interval)
	}

	// Start peer connections in background
	go relayServer.ConnectPeers(ctx)

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

func envDuration(key string, defaultVal time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	return d, nil
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
	// INSECURE mode: generate a self-signed certificate on the fly.
	// Never use in production.
	if os.Getenv("INSECURE") == "true" {
		cert, err := generateSelfSignedCert()
		if err != nil {
			return nil, fmt.Errorf("failed to generate self-signed cert: %w", err)
		}
		slog.Warn("INSECURE mode: using ephemeral self-signed certificate")
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h3", moqt.NextProtoMOQ},
		}, nil
	}

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

// generateSelfSignedCert creates an in-memory ECDSA P-256 self-signed certificate
// valid for 365 days. Only used when INSECURE=true.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}
