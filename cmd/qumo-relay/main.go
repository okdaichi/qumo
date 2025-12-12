package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/relay"
	"gopkg.in/yaml.v3"
)

type config struct {
	Address     string
	CertFile    string
	KeyFile     string
	Upstream    string
	RelayConfig relay.Config
}

func main() {
	var configFile = flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	// Load configuration
	config, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Setup TLS
	tlsConfig, err := setupTLS(config.CertFile, config.KeyFile)
	if err != nil {
		log.Fatalf("Failed to setup TLS: %v", err)
	}

	// Apply relay configuration
	relay.NewFrameCapacity = config.RelayConfig.FrameCapacity
	relay.GroupCacheSize = config.RelayConfig.GroupCacheSize

	log.Printf("Starting qumo-relay server on %s", config.Address)

	// Setup signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create MOQT server with correct API
	server := &moqt.Server{
		Addr:      config.Address,
		TLSConfig: tlsConfig,
		// Accept all incoming sessions
		SetupHandler: moqt.SetupHandlerFunc(relay.Serve),
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	log.Println("Server started successfully")

	// Wait for shutdown signal
	<-ctx.Done()
	log.Println("Shutting down server...")

	// Graceful shutdown
	if err := server.Shutdown(context.Background()); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Server stopped")
}

func loadConfig(filename string) (*config, error) {
	type yamlConfig struct {
		Server struct {
			Address  string `yaml:"address"`
			CertFile string `yaml:"cert_file"`
			KeyFile  string `yaml:"key_file"`
		} `yaml:"server"`
		Relay struct {
			Upstream       string `yaml:"upstream"`
			GroupCacheSize int    `yaml:"group_cache_size"`
			FrameCapacity  int    `yaml:"frame_capacity"`
		} `yaml:"relay"`
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
		Upstream: ymlConfig.Relay.Upstream,
		RelayConfig: relay.Config{
			Upstream:       ymlConfig.Relay.Upstream,
			FrameCapacity:  ymlConfig.Relay.FrameCapacity,
			GroupCacheSize: ymlConfig.Relay.GroupCacheSize,
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
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"}, // HTTP/3 for QUIC
	}, nil
}
