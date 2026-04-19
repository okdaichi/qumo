package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/bootstrap"
	"github.com/okdaichi/qumo/internal/relay"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
)

// RunRelay starts the MoQ relay server.
//
// Configuration is read from environment variables:
//
//	RELAY_ADDR          - listen address (default: "0.0.0.0:4433")
//	CERT_FILE           - TLS certificate file (default: "certs/server.crt")
//	KEY_FILE            - TLS key file (default: "certs/server.key")
//	RELAY_NAME          - node ID (default: "relay-" + hostname)
//	REGION              - geographic region (default: "")
//	ROLE                - node role: "hub" or "edge" (default: "")
//	ADVERTISE_ADDR      - address advertised to peers (required when RELAY_ADDR is wildcard)
//	GROUP_CACHE_SIZE    - max group caches (default: 100) [TODO: not yet wired to handler]
//	FRAME_CAPACITY      - frame buffer size in bytes (default: 1500) [TODO: not yet wired to handler]
//	PEERS               - comma-separated list of peer addresses
//	BOOTSTRAP_URLS      - comma-separated list of bootstrap server URLs
//	BOOTSTRAP_INTERVAL  - polling interval for bootstrap servers (default: "15s")
func RunRelay(_ []string) error {
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

	var peers []relay.Peer
	if raw := os.Getenv("PEERS"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, relay.Peer{Address: p})
			}
		}
	}

	var bootstraps []bootstrap.ClientConfig
	if raw := os.Getenv("BOOTSTRAP_URLS"); raw != "" {
		intervalStr := envOr("BOOTSTRAP_INTERVAL", "15s")
		interval, parseErr := time.ParseDuration(intervalStr)
		if parseErr != nil {
			return fmt.Errorf("invalid BOOTSTRAP_INTERVAL %q: %w", intervalStr, parseErr)
		}
		for _, u := range strings.Split(raw, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				bootstraps = append(bootstraps, bootstrap.ClientConfig{
					URL:      u,
					Interval: interval,
				})
			}
		}
	}

	relayCfg := relay.Config{
		NodeID:         nodeID,
		Region:         os.Getenv("REGION"),
		Role:           os.Getenv("ROLE"),
		AdvertiseAddr:  advertiseAddr,
		GroupCacheSize: groupCacheSize,
		FrameCapacity:  frameCapacity,
		Peers:          peers,
		Bootstraps:     bootstraps,
	}

	// Setup TLS
	tlsConfig, err := setupTLS(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to setup TLS: %w", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create relay relayServer
	trackMux := moqt.NewTrackMux(moqt.NewHopID())
	relayServer := &relay.Server{
		Addr:      addr,
		TLSConfig: tlsConfig,
		QUICConfig: &quic.Config{
			Allow0RTT:                        true,
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
		Config:   &relayCfg,
		TrackMux: trackMux,
	}

	httpMux := http.NewServeMux()
	wtPath := "/"
	relayServer.RouteWebTransport(wtPath, httpMux)
	httpMux.Handle("/health", &healthHandler{
		statusFunc: relayServer.Status,
	})
	httpMux.Handle("/metrics", promhttp.Handler())

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	hostLog := strings.ReplaceAll(strings.ReplaceAll(addr, "\r", ""), "\n", "")
	advertiseLog := strings.ReplaceAll(strings.ReplaceAll(relayCfg.AdvertiseAddr, "\r", ""), "\n", "")
	nodeIDLog := strings.ReplaceAll(strings.ReplaceAll(relayCfg.NodeID, "\r", ""), "\n", "")
	regionLog := strings.ReplaceAll(strings.ReplaceAll(relayCfg.Region, "\r", ""), "\n", "")

	log.Printf("\t%-8s: %s\n", "Host", hostLog)
	log.Printf("\t%-8s: %s\n", "Advertise", advertiseLog)
	log.Printf("\t%-8s: %s\n", "Node ID", nodeIDLog)
	log.Printf("\t%-8s: %s\n", "Region", regionLog)
	log.Printf("\t%-8s: WebTransport endpoint\n", wtPath)
	log.Printf("\t%-8s: liveness/readiness probe\n", "/health")
	log.Printf("\t%-8s: Prometheus metrics\n", "/metrics")
	for _, p := range relayCfg.Peers {
		peerLog := strings.ReplaceAll(strings.ReplaceAll(p.Address, "\r", ""), "\n", "")
		log.Printf("\t%-8s: %s\n", "Peer", peerLog)
	}
	for _, b := range relayCfg.Bootstraps {
		bootstrapURL := strings.ReplaceAll(strings.ReplaceAll(b.URL, "\r", ""), "\n", "")
		log.Printf("\t%-8s: %s (interval: %s)\n", "Bootstrap", bootstrapURL, b.Interval)
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

// server is a minimal interface implemented by both *relay.Server and
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

type healthHandler struct {
	statusFunc func() relay.Status
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// single handler that supports probes via query param: ?probe=live|ready
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	probe := r.URL.Query().Get("probe")

	switch probe {
	case "live":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
		return

	case "ready":
		status := h.statusFunc()
		activeConns := status.ActiveConnections

		ready := true
		reason := "ready"

		if activeConns < 0 {
			ready = false
			reason = "invalid_connection_state"
		}

		statusCode := http.StatusOK
		if !ready {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if r.Method == http.MethodHead {
			return
		}

		response := map[string]any{"ready": ready}
		if !ready {
			response["reason"] = reason
		}
		_ = json.NewEncoder(w).Encode(response)
		return

	default:
		// full status
		status := h.statusFunc()

		ready := true
		reason := "ready"
		if status.ActiveConnections < 0 {
			ready = false
			reason = "invalid_connection_state"
		}

		response := map[string]any{
			"status":             status.Status,
			"timestamp":          status.Timestamp,
			"uptime":             status.Uptime,
			"active_connections": status.ActiveConnections,
			"live":               true,
			"ready":              ready,
		}
		if !ready {
			response["ready_reason"] = reason
		}

		statusCode := http.StatusOK
		if status.Status == "unhealthy" {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}
}
