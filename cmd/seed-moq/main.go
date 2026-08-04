// Command seed-moq runs a minimal MoQ server that publishes a live CMAF (fMP4)
// track, so the HLS egress (`qumo hls`) can be exercised end-to-end — MoQ
// catalog + CMAF groups into the ledger, served back as HLS — without a full
// relay and publisher.
//
// It serves the MSF catalog (carrying the track's fMP4 init in initData) on the
// reserved "catalog" track and publishes one placeholder CMAF fragment per
// interval. Payloads and the init are placeholders — the playlist is
// structurally valid (and carries #EXT-X-MAP) but not playable; real fMP4
// comes from a WebCodecs publisher.
//
// It generates an ephemeral self-signed certificate, so point the egress at it
// with RELAY_TLS_INSECURE=true (the egress default).
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/msf"
)

func main() {
	addr := envOr("SEED_ADDR", ":4433")
	path := envOr("SEED_TRACK_PATH", "/live/cam1")
	trackName := envOr("SEED_TRACK_NAME", "video")
	interval := envDuration("SEED_GROUP_INTERVAL", 2*time.Second)
	timescale := envIntOr("SEED_TIMESCALE", 90000)

	tlsConfig, err := selfSignedTLS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-moq:", err)
		os.Exit(1)
	}
	tlsConfig.NextProtos = []string{"h3", moqt.NextProtoMOQ}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mux := moqt.NewTrackMux(0)

	broadcast, err := msf.NewBroadcast(msf.Catalog{Version: 1})
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-moq:", err)
		os.Exit(1)
	}
	// The catalog carries the track's packaging (CMAF/fMP4) and its init in
	// initData; the egress reads both to build the ledger schema and the HLS
	// init segment.
	if err := broadcast.RegisterTrack(msf.Track{
		Name:      trackName,
		Packaging: msf.PackagingCMAF,
		InitData:  base64.StdEncoding.EncodeToString([]byte("FAKE-FMP4-INIT")),
		Timescale: ptrInt64(int64(timescale)),
		Codec:     "avc1.42c01e",
		Role:      msf.RoleVideo,
		IsLive:    ptrBool(true),
	}, moqt.TrackHandlerFunc(func(tw *moqt.TrackWriter) {
		publish(ctx, tw, interval)
	})); err != nil {
		fmt.Fprintln(os.Stderr, "seed-moq:", err)
		os.Exit(1)
	}

	// The broadcast is itself a TrackHandler: it serves the catalog track and
	// dispatches media-track subscribes to the registered handler.
	mux.Publish(ctx, moqt.BroadcastPath(path), broadcast)

	httpMux := http.NewServeMux()
	httpMux.Handle("/", &moqt.WebTransportHandler{
		TrackMux:    mux,
		CheckOrigin: func(*http.Request) bool { return true },
		Handler: moqt.HandleFunc(func(sess *moqt.Session) {
			defer sess.CloseWithError(moqt.NoError, moqt.NoError.String())
			<-sess.Context().Done()
		}),
	})

	srv := &moqt.Server{
		Addr:               addr,
		TLSConfig:          tlsConfig,
		WebTransportServer: moqt.NewWebTransportServer(httpMux),
		TrackMux:           mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		// not actionable: a shutdown error only reports a closed listener on exit.
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("seed-moq: serving", "addr", addr, "path", path, "track", trackName, "interval", interval)
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "seed-moq:", err)
		os.Exit(1)
	}
}

// publish streams a live track to one subscriber: a placeholder CMAF fragment
// every interval, each carrying a single small frame. It returns when ctx is
// cancelled or the subscriber disconnects.
func publish(ctx context.Context, tw *moqt.TrackWriter, interval time.Duration) {
	slog.Info("seed-moq: subscriber connected")
	defer slog.Info("seed-moq: subscriber gone")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tw.Context().Done():
			return
		case <-ticker.C:
		}

		gw, err := tw.OpenGroupAt(ctx, moqt.GroupSequence(seq))
		if err != nil {
			return
		}
		frame := moqt.NewFrame(32)
		// not actionable: Write only fails on a nil frame, which NewFrame is not.
		_, _ = frame.Write([]byte(fmt.Sprintf("group-%d", seq)))
		if err := gw.WriteFrame(frame); err != nil {
			_ = gw.Close()
			return
		}
		_ = gw.Close()
		seq++
	}
}

// selfSignedTLS builds an ephemeral self-signed certificate valid for localhost
// and the loopback IPs, so the server runs with no on-disk cert material.
func selfSignedTLS() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func ptrInt64(v int64) *int64 { return &v }
func ptrBool(v bool) *bool    { return &v }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v == "" {
		return def
	} else if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
