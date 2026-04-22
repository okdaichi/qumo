package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qumo-dev/qumo/internal/bootstrap"
)

// RunBootstrap starts the MoQ bootstrap server for node registration and peer discovery.
//
// Configuration is read from environment variables:
//
//	BOOTSTRAP_ADDR              - address to listen on (default: ":8080")
//	BOOTSTRAP_TTL               - node TTL before expiration (default: "30s")
//	BOOTSTRAP_CLEANUP_INTERVAL  - interval between cleanup sweeps (default: "5s")
//	BOOTSTRAP_MAX_PEERS         - maximum number of peers to return (default: 20)
//	BOOTSTRAP_AUTH_TOKEN        - bearer token required on /register and /peers (optional)
//	BOOTSTRAP_CERT_FILE         - TLS certificate for HTTPS; enables TLS when set
//	BOOTSTRAP_KEY_FILE          - TLS private key for HTTPS (required with BOOTSTRAP_CERT_FILE)
//	CA_FILE                     - PEM CA cert; enables mTLS client verification when set
//	                               (requires BOOTSTRAP_CERT_FILE / BOOTSTRAP_KEY_FILE)
func RunBootstrap(_ []string) error {
	listen := envOr("BOOTSTRAP_ADDR", ":8080")

	ttl, err := envDuration("BOOTSTRAP_TTL", 30*time.Second)
	if err != nil {
		return fmt.Errorf("invalid BOOTSTRAP_TTL: %w", err)
	}
	cleanupInterval, err := envDuration("BOOTSTRAP_CLEANUP_INTERVAL", 5*time.Second)
	if err != nil {
		return fmt.Errorf("invalid BOOTSTRAP_CLEANUP_INTERVAL: %w", err)
	}
	authToken := os.Getenv("BOOTSTRAP_AUTH_TOKEN")

	// TLS / mTLS setup for the bootstrap server.
	// TLS is enabled when BOOTSTRAP_CERT_FILE and BOOTSTRAP_KEY_FILE are set.
	// mTLS (client cert verification) is additionally enabled when CA_FILE is set.
	bootstrapCertFile := os.Getenv("BOOTSTRAP_CERT_FILE")
	bootstrapKeyFile := os.Getenv("BOOTSTRAP_KEY_FILE")
	var srvTLSConfig *tls.Config
	if bootstrapCertFile != "" || bootstrapKeyFile != "" {
		if bootstrapCertFile == "" || bootstrapKeyFile == "" {
			return fmt.Errorf("BOOTSTRAP_CERT_FILE and BOOTSTRAP_KEY_FILE must both be set for TLS")
		}
		srvTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if caPool, caErr := loadCACertPool(os.Getenv("CA_FILE")); caErr != nil {
			return fmt.Errorf("failed to load CA_FILE: %w", caErr)
		} else if caPool != nil {
			clientAuth := tls.VerifyClientCertIfGiven
			if os.Getenv("MTLS_REQUIRED") == "true" {
				clientAuth = tls.RequireAndVerifyClientCert
			}
			srvTLSConfig.ClientAuth = clientAuth
			srvTLSConfig.ClientCAs = caPool
			slog.Info("bootstrap mTLS enabled",
				"ca_file", os.Getenv("CA_FILE"),
				"strict", clientAuth == tls.RequireAndVerifyClientCert,
			)
		}
	}

	// Warn when mTLS is optional (VerifyClientCertIfGiven) and no bearer token is configured:
	// clients without a certificate are accepted with no authentication at all.
	if srvTLSConfig != nil && srvTLSConfig.ClientAuth == tls.VerifyClientCertIfGiven && authToken == "" {
		slog.Warn("bootstrap: mTLS is optional and BOOTSTRAP_AUTH_TOKEN is not set; " +
			"clients without a certificate will be accepted unauthenticated — " +
			"set MTLS_REQUIRED=true or BOOTSTRAP_AUTH_TOKEN to enforce authentication")
	}

	maxPeers, err := envInt("BOOTSTRAP_MAX_PEERS", 20)
	if err != nil {
		return fmt.Errorf("invalid BOOTSTRAP_MAX_PEERS: %w", err)
	}

	store := bootstrap.NewStore(ttl)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store.StartCleaner(ctx, cleanupInterval)

	mux := http.NewServeMux()
	mux.Handle("/register", &bootstrap.RegisterHandler{
		Store:     store,
		AuthToken: authToken,
	})
	mux.Handle("/peers", &bootstrap.PeersHandler{Store: store, MaxPeers: maxPeers, AuthToken: authToken})

	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         srvTLSConfig,
	}

	slog.Info("bootstrap server starting",
		"listen", listen,
		"ttl", ttl,
		"cleanup_interval", cleanupInterval,
		"max_peers", maxPeers,
		"tls", srvTLSConfig != nil,
	)

	// Run server in a goroutine; block on ctx cancellation.
	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if srvTLSConfig != nil {
			serveErr = srv.ListenAndServeTLS(bootstrapCertFile, bootstrapKeyFile)
		} else {
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http ListenAndServe: %w", serveErr)
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down bootstrap server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}
	}

	slog.Info("bootstrap server stopped")
	return nil
}
