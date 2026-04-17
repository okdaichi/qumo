package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/okdaichi/qumo/internal/bootstrap"
)

// RunBootstrap starts the MoQ bootstrap server for node registration and peer discovery.
func RunBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "address to listen on")
	ttl := fs.Duration("ttl", 30*time.Second, "node TTL before expiration")
	cleanupInterval := fs.Duration("cleanup-interval", 5*time.Second, "interval between cleanup sweeps")
	maxPeers := fs.Int("max-peers", 20, "maximum number of peers to return")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	store := bootstrap.NewStore(*ttl)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store.StartCleaner(ctx, *cleanupInterval)

	mux := http.NewServeMux()
	mux.Handle("/register", &bootstrap.RegisterHandler{Store: store})
	mux.Handle("/peers", &bootstrap.PeersHandler{Store: store, MaxPeers: *maxPeers})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("bootstrap server starting",
		"listen", *listen,
		"ttl", *ttl,
		"cleanup_interval", *cleanupInterval,
		"max_peers", *maxPeers,
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
