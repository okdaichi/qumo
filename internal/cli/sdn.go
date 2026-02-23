package cli

import (
	"context"
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

	"github.com/okdaichi/qumo/internal/sdn"
	"github.com/okdaichi/qumo/internal/topology"
	"gopkg.in/yaml.v3"
)

type sdnConfig struct {
	ListenAddr   string
	DataDir      string
	PeerURL      string
	SyncInterval time.Duration
	NodeTTL      time.Duration
}

const defaultAddr = ":8090"
const defaultSyncInterval = 10 * time.Second

// RunSDN starts the SDN routing controller.
func RunSDN(args []string) error {
	fs := flag.NewFlagSet("sdn", flag.ExitOnError)
	var configFile = fs.String("config", "config.sdn.yaml", "path to config file")
	fs.Parse(args)

	cfg, err := loadSDNConfig(*configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	topo := &topology.Topology{
		NodeTTL: cfg.NodeTTL,
	}

	// Configure persistence (optional)
	if cfg.DataDir != "" {
		topo.Store = topology.NewFileStore(cfg.DataDir + "/topology.json")
		log.Printf("Persistence enabled: %s/topology.json", cfg.DataDir)
	}

	announceTable := sdn.NewAnnounceTable(90 * time.Second)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start background sweeper to remove expired announces
	announceTable.StartSweeper(ctx, 30*time.Second)

	// Start topology sweeper to remove stale relay nodes
	topo.StartSweeper(ctx, 30*time.Second)

	mux := http.NewServeMux()

	// Topology + Relay registration routes
	mux.HandleFunc("/relay/", topology.NewNodeHandlerFunc(topo))
	mux.HandleFunc("/route", topology.RouteHandlerFunc(topo))
	mux.HandleFunc("/graph", topology.GraphHandlerFunc(topo))
	mux.HandleFunc("/sync", topology.SyncHandlerFunc(topo))

	// Announce table routes
	mux.HandleFunc("/announce/lookup", sdn.LookupHandlerFunc(announceTable))
	mux.HandleFunc("/announce/", sdn.HandlerFunc(announceTable))
	mux.HandleFunc("/announce", sdn.ListHandlerFunc(announceTable))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// P5: Start peer syncer if configured.
	if cfg.PeerURL != "" {
		syncInterval := cfg.SyncInterval
		if syncInterval <= 0 {
			syncInterval = defaultSyncInterval
		}
		syncer := topology.NewPeerSyncer(cfg.PeerURL, topo, syncInterval)
		go syncer.Run(ctx)

		log.Printf("HA peer sync enabled: %s every %s", cfg.PeerURL, syncInterval)
	}

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Printf("SDN routing controller started on %s", cfg.ListenAddr)
	log.Println("  /relay/<name>   - PUT: register relay (cost+load), DELETE: deregister")
	log.Println("  /route          - GET: compute route (?from=X&to=Y)")
	log.Println("  /graph          - GET: current topology")
	log.Println("  /announce/...   - PUT/DELETE: track announcements")
	log.Println("  /announce/lookup - GET: find relays by track")
	log.Println("  /announce       - GET: list all announcements")
	log.Println("  /sync           - GET/PUT: HA topology sync")
	log.Println("  /health         - Health check")

	<-ctx.Done()
	cancel()

	slog.Info("Shutting down SDN routing controller...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}

	slog.Info("SDN routing controller stopped")
	return nil
}

func loadSDNConfig(filename string) (*sdnConfig, error) {
	type yamlConfig struct {
		Graph struct {
			ListenAddr   string `yaml:"listen_addr"`
			DataDir      string `yaml:"data_dir"`
			PeerURL      string `yaml:"peer_url"`
			SyncInterval int    `yaml:"sync_interval_sec"`
			NodeTTLSec   int    `yaml:"node_ttl_sec"`
		} `yaml:"graph"`
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var ymlCfg yamlConfig
	if err := yaml.NewDecoder(file).Decode(&ymlCfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	listenAddr := ymlCfg.Graph.ListenAddr
	if listenAddr == "" {
		listenAddr = ":8090"
	}

	return &sdnConfig{
		ListenAddr:   listenAddr,
		DataDir:      ymlCfg.Graph.DataDir,
		PeerURL:      ymlCfg.Graph.PeerURL,
		SyncInterval: time.Duration(ymlCfg.Graph.SyncInterval) * time.Second,
		NodeTTL:      time.Duration(ymlCfg.Graph.NodeTTLSec) * time.Second,
	}, nil
}
