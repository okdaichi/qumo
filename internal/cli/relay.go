package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/relay"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
	"gopkg.in/yaml.v3"
)

type config struct {
	Address     string
	CertFile    string
	KeyFile     string
	RelayConfig relay.Config
}

func RunRelay(args []string) error {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	var configFile = fs.String("config", "config.relay.yaml", "path to config file")
	fs.Parse(args)

	// Load configuration
	config, err := loadConfig(*configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Setup TLS
	tlsConfig, err := setupTLS(config.CertFile, config.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to setup TLS: %w", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create relay relayServer
	trackMux := moqt.NewTrackMux()
	relayServer := &relay.Server{
		Addr:      config.Address,
		TLSConfig: tlsConfig,
		QUICConfig: &quic.Config{
			Allow0RTT:                        true,
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
		Config:   &config.RelayConfig,
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
		Addr:              config.Address,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("\t%-8s: %s\n", "Host", config.Address)
	log.Printf("\t%-8s: %s\n", "Node ID", config.RelayConfig.NodeID)
	log.Printf("\t%-8s: %s\n", "Region", config.RelayConfig.Region)
	log.Printf("\t%-8s: WebTransport endpoint\n", wtPath)
	log.Printf("\t%-8s: liveness/readiness probe\n", "/health")
	log.Printf("\t%-8s: Prometheus metrics\n", "/metrics")
	for _, p := range config.RelayConfig.Peers {
		log.Printf("\t%-8s: %s\n", "Peer", p.Address)
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

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
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

func loadConfig(filename string) (*config, error) {
	type yamlConfig struct {
		Server struct {
			Address  string `yaml:"address"`
			CertFile string `yaml:"cert_file"`
			KeyFile  string `yaml:"key_file"`
		} `yaml:"server"`
		Relay struct {
			NodeID         string `yaml:"node_id"`
			Region         string `yaml:"region"`
			GroupCacheSize int    `yaml:"group_cache_size"`
			FrameCapacity  int    `yaml:"frame_capacity"`
		} `yaml:"relay"`
		Peers []struct {
			Address string `yaml:"address"`
		} `yaml:"peers"`
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var ymlConfig yamlConfig
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&ymlConfig); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	// Set defaults
	if ymlConfig.Relay.FrameCapacity == 0 {
		ymlConfig.Relay.FrameCapacity = 1500
	}
	if ymlConfig.Relay.GroupCacheSize == 0 {
		ymlConfig.Relay.GroupCacheSize = 100
	}

	var peers []relay.Peer
	for _, p := range ymlConfig.Peers {
		peers = append(peers, relay.Peer{Address: p.Address})
	}

	config := &config{
		Address:  ymlConfig.Server.Address,
		CertFile: ymlConfig.Server.CertFile,
		KeyFile:  ymlConfig.Server.KeyFile,
		RelayConfig: relay.Config{
			NodeID:         ymlConfig.Relay.NodeID,
			Region:         ymlConfig.Relay.Region,
			FrameCapacity:  ymlConfig.Relay.FrameCapacity,
			GroupCacheSize: ymlConfig.Relay.GroupCacheSize,
			Peers:          peers,
		},
	}

	return config, nil
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
