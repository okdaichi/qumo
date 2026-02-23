package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/relay"
	"github.com/okdaichi/qumo/internal/sdn"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

type config struct {
	Address     string
	CertFile    string
	KeyFile     string
	MetricsAddr string
	AdminAddr   string
	RelayConfig relay.Config
	SDNConfig   *sdn.ClientConfig // nil if auto-announce is disabled
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
		Config:    &config.RelayConfig,
		TrackMux:  trackMux,
		CheckHTTPOrigin: func(r *http.Request) bool {
			return true //TODO:
		},
	}

	// Set up SDN auto-announce client if configured
	if config.SDNConfig != nil {
		var err error
		sdnClient, err := sdn.NewClient(*config.SDNConfig)
		if err != nil {
			return fmt.Errorf("failed to create SDN client: %w", err)
		}
		relayServer.AnnounceRegistrar = sdnClient
		go sdnClient.Run(ctx)

		// Start remote fetcher to discover and subscribe to remote broadcasts
		fetcher := &relay.RemoteFetcher{
			SDNClient:      sdnClient,
			TrackMux:       trackMux,
			TLSConfig:      tlsConfig,
			GroupCacheSize: config.RelayConfig.GroupCacheSize,
		}
		go fetcher.Run(ctx)
	}

	mux := http.NewServeMux()
	mux.Handle("/health", &healthHandler{
		statusFunc: relayServer.Status,
	})
	mux.Handle("/metrics", promhttp.Handler())

	httpServer := &http.Server{
		Addr:    config.Address,
		Handler: mux,
	}

	// Delegate to testable helper that runs servers until ctx is cancelled
	serveComponents(ctx, relayServer, httpServer, 10*time.Second)

	return nil
}

// serverRunner is a minimal interface implemented by both *relay.Server and
// *http.Server so we can unit-test the run/shutdown flow with fakes.
type serverRunner interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// serveComponents starts the provided servers and blocks until ctx is cancelled.
// It intentionally mirrors the previous RunRelay behavior: ListenAndServe
// errors are logged but do not abort the shutdown sequence.
func serveComponents(ctx context.Context, relaySrv serverRunner, httpSrv serverRunner, shutdownTimeout time.Duration) {
	// Start servers (errors from ListenAndServe are logged but ignored here)
	go func() {
		if err := relaySrv.ListenAndServe(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil {
			if err == http.ErrServerClosed {
				return // Normal shutdown
			}
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Println("Server started successfully")
	log.Println("  /             - WebTransport & MoQ endpoint")
	log.Println("  /health       - Health check (?probe=live|ready)")
	log.Println("  /metrics      - Prometheus metrics")

	// Wait for cancellation
	<-ctx.Done()

	slog.Info("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := relaySrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down http server: %v", err)
	}

	slog.Info("Server stopped")
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
		SDN *struct {
			URL               string `yaml:"url"`
			RelayName         string `yaml:"relay_name"`
			HeartbeatInterval int    `yaml:"heartbeat_interval_sec"`
			TLS               *struct {
				CertFile string `yaml:"cert_file"`
				KeyFile  string `yaml:"key_file"`
				CAFile   string `yaml:"ca_file"`
			} `yaml:"tls"`
		} `yaml:"sdn"`
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

	config := &config{
		Address:  ymlConfig.Server.Address,
		CertFile: ymlConfig.Server.CertFile,
		KeyFile:  ymlConfig.Server.KeyFile,
		RelayConfig: relay.Config{
			NodeID:         ymlConfig.Relay.NodeID,
			Region:         ymlConfig.Relay.Region,
			FrameCapacity:  ymlConfig.Relay.FrameCapacity,
			GroupCacheSize: ymlConfig.Relay.GroupCacheSize,
		},
	}

	// Parse optional SDN auto-announce config
	if ymlConfig.SDN != nil && ymlConfig.SDN.URL != "" {
		sdnCfg := &sdn.ClientConfig{
			URL:       ymlConfig.SDN.URL,
			RelayName: ymlConfig.SDN.RelayName,
		}
		if sdnCfg.RelayName == "" {
			sdnCfg.RelayName = ymlConfig.Relay.NodeID
		}
		if ymlConfig.SDN.HeartbeatInterval > 0 {
			sdnCfg.HeartbeatInterval = time.Duration(ymlConfig.SDN.HeartbeatInterval) * time.Second
		}
		if ymlConfig.SDN.TLS != nil {
			sdnCfg.TLS = &sdn.TLSConfig{
				CertFile: ymlConfig.SDN.TLS.CertFile,
				KeyFile:  ymlConfig.SDN.TLS.KeyFile,
				CAFile:   ymlConfig.SDN.TLS.CAFile,
			}
		}
		config.SDNConfig = sdnCfg
	}

	return config, nil
}

func setupTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificates: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3", "moq-00"}, // HTTP/3 for WebTransport, MOQ native QUIC
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
		json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
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
		json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
		return
	}
}
