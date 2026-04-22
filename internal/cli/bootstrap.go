package cli

import (
	"context"
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
//	BOOTSTRAP_ADDR            - address to listen on (default: ":8080")
//	BOOTSTRAP_TTL               - node TTL before expiration (default: "30s")
//	BOOTSTRAP_CLEANUP_INTERVAL  - interval between cleanup sweeps (default: "5s")
//	BOOTSTRAP_MAX_PEERS         - maximum number of peers to return (default: 20)
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

	maxPeers, err := envInt("BOOTSTRAP_MAX_PEERS", 20)
	if err != nil {
		return fmt.Errorf("invalid BOOTSTRAP_MAX_PEERS: %w", err)
	}

	store := bootstrap.NewStore(ttl)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store.StartCleaner(ctx, cleanupInterval)

	mux := http.NewServeMux()
	mux.Handle("/register", &bootstrap.RegisterHandler{Store: store, AuthToken: authToken})
	mux.Handle("/peers", &bootstrap.PeersHandler{Store: store, MaxPeers: maxPeers, AuthToken: authToken})

	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("bootstrap server starting",
		"listen", listen,
		"ttl", ttl,
		"cleanup_interval", cleanupInterval,
		"max_peers", maxPeers,
	)

	// Run server in a goroutine; block on ctx cancellation.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http ListenAndServe: %w", err)
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
