package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/ingest"
	"gopkg.in/yaml.v3"
)

type rtmpConfig struct {
	IngestAddr string
	ServeAddr  string
	CertFile   string
	KeyFile    string
}

const (
	defaultRTMPIngestAddr = ":1935"
	defaultRTMPServeAddr  = ":4433"
)

// RunRTMP starts a standalone RTMP ingest server that bridges published
// streams to MoQT. Unlike the relay command this does not participate in
// the mesh (no peer connections, no announce relay).
func RunRTMP(args []string) error {
	fs := flag.NewFlagSet("rtmp", flag.ExitOnError)
	configFile := fs.String("config", "config.rtmp.yaml", "path to config file")
	fs.Parse(args)

	cfg, err := loadRTMPConfig(*configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trackMux := moqt.NewTrackMux()

	// RTMP ingest server
	rtmpSrv := ingest.NewRTMPServer(ingest.RTMPConfig{
		Addr:     cfg.IngestAddr,
		TrackMux: trackMux,
	})

	// Minimal MoQT origin that serves subscribers from the shared TrackMux.
	moqtSrv := &moqt.Server{
		Addr: cfg.ServeAddr,
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())
			<-sess.Context().Done()
		}),
	}

	log.Println("	Ingest  :", cfg.IngestAddr)
	log.Println("	Serve   :", cfg.ServeAddr)

	// Start RTMP ingest
	go func() {
		if err := rtmpSrv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Error("RTMP server error", "err", err)
			cancel()
		}
	}()

	// Start MoQT origin (QUIC)
	go func() {
		if err := moqtSrv.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil && ctx.Err() == nil {
			slog.Error("MoQT server error", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	rtmpSrv.Shutdown(shutdownCtx)
	moqtSrv.Shutdown(shutdownCtx)

	return nil
}

func loadRTMPConfig(filename string) (*rtmpConfig, error) {
	type yamlRTMPConfig struct {
		Server struct {
			ServeAddr string `yaml:"serve_address"`
			MoQTAddr  string `yaml:"moqt_address"`
			CertFile  string `yaml:"cert_file"`
			KeyFile   string `yaml:"key_file"`
		} `yaml:"server"`
		Ingest struct {
			IngestAddr string `yaml:"ingest_address"`
			RTMPAddr   string `yaml:"rtmp_address"`
		} `yaml:"ingest"`
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var yc yamlRTMPConfig
	if err := yaml.NewDecoder(file).Decode(&yc); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	cfg := &rtmpConfig{
		IngestAddr: yc.Ingest.IngestAddr,
		ServeAddr:  yc.Server.ServeAddr,
		CertFile:   yc.Server.CertFile,
		KeyFile:    yc.Server.KeyFile,
	}

	cfg.IngestAddr = firstNonEmpty(cfg.IngestAddr, yc.Ingest.RTMPAddr, defaultRTMPIngestAddr)
	cfg.ServeAddr = firstNonEmpty(cfg.ServeAddr, yc.Server.MoQTAddr, defaultRTMPServeAddr)

	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
