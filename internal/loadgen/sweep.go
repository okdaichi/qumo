package loadgen

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// runSweep runs a session-count sweep: it (optionally) starts a local relay
// subprocess, runs a background publisher, and for each session count measures
// subscribers and records the relay's hold. Only the relay is a separate
// process; the publisher and subscribers share this (load) process — which is
// the point, since it keeps client cost off the relay's cores. Point --relay at
// a remote host (with --ca) for a true two-host run.
func runSweep(args []string) error {
	fs := flag.NewFlagSet("loadgen sweep", flag.ContinueOnError)
	relay := fs.String("relay", "127.0.0.1:4433", "relay moqt address (host:port)")
	metrics := fs.String("metrics", "", "relay /metrics URL (default http://<relay>/metrics)")
	caFile := fs.String("ca", "", "relay cert/CA to trust (required unless --start-relay)")
	path := fs.String("path", "/bench/carry", "broadcast path")
	track := fs.String("track", "data", "track name")
	idle := fs.Duration("idle-timeout", 30*time.Second, "QUIC max idle timeout")
	keepalive := fs.Duration("keepalive", 5*time.Second, "QUIC keep-alive period")
	sessionsArg := fs.String("sessions", "2000 5000 8000 12000", "session counts to sweep (space/comma-separated)")
	hold := fs.Duration("hold", 15*time.Second, "hold duration per point")
	gps := fs.Float64("gps", 0.5, "publisher groups per second")
	size := fs.Int("size", 64, "frame size in bytes")
	results := fs.String("results", "", "dir to append capacity JSONL records (optional)")
	startRelay := fs.Bool("start-relay", false, "start a local relay subprocess (self-signed cert generated in-process)")
	relayBin := fs.String("relay-bin", "", "relay binary for --start-relay (default: this executable)")
	relayCores := fs.String("relay-cores", "", "taskset CPU list for the relay subprocess (Linux; --start-relay)")
	gogc := fs.Int("gogc", 800, "GOGC for the relay subprocess (--start-relay)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sessions, err := parseSessions(*sessionsArg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var target dialTarget
	if *startRelay {
		tmp, err := os.MkdirTemp("", "loadgen-relay-")
		if err != nil {
			return fmt.Errorf("temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		certFile, keyFile, pool, err := generateRelayCert(tmp)
		if err != nil {
			return err
		}
		stop, err := startRelaySubprocess(ctx, relaySubprocess{
			bin: *relayBin, addr: *relay, certFile: certFile, keyFile: keyFile, cores: *relayCores, gogc: *gogc,
		})
		if err != nil {
			return err
		}
		defer stop()
		target = newTarget(*relay, *metrics, *path, *track, pool, *idle, *keepalive)
	} else {
		if *caFile == "" {
			return errors.New("--ca is required unless --start-relay")
		}
		pool, err := loadCAPool(*caFile)
		if err != nil {
			return err
		}
		target = newTarget(*relay, *metrics, *path, *track, pool, *idle, *keepalive)
	}

	if err := waitForMetrics(ctx, target.metrics, 30*time.Second); err != nil {
		return err
	}

	// Background publisher (this load process; only the relay is separate).
	pubCtx, pubCancel := context.WithCancel(ctx)
	defer pubCancel()
	pubErr := make(chan error, 1)
	go func() { pubErr <- publishTrickle(pubCtx, target, *gps, *size) }()
	sleepCtx(ctx, 2*time.Second) // let the announcement propagate
	select {
	case err := <-pubErr:
		if err != nil {
			return fmt.Errorf("publisher: %w", err)
		}
	default:
	}

	for _, s := range sessions {
		if ctx.Err() != nil {
			break
		}
		res, err := runCarry(ctx, target, s, *hold)
		if err != nil {
			return err
		}
		report(target, s, res)
		if *results != "" {
			if err := emitJSONL(*results, s, res); err != nil {
				slog.Warn("loadgen results emission failed", "err", err)
			}
		}
	}
	return nil
}

// parseSessions parses a space/comma-separated list of positive session counts.
func parseSessions(s string) ([]int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })
	if len(fields) == 0 {
		return nil, errors.New("no session counts given")
	}
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid session count %q (want a positive integer)", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// waitForMetrics polls the relay /metrics endpoint until it answers or timeout.
func waitForMetrics(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := fetchMetrics(ctx, client, url); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("relay /metrics did not come up at %s within %s", url, timeout)
}

// generateRelayCert writes a fresh self-signed ECDSA cert+key (SAN localhost,
// 127.0.0.1, ::1) into dir and returns their paths plus a pool that trusts it.
// Self-signed = its own issuer, so the same PEM is both the server cert and the
// client's trust anchor — no openssl dependency.
func generateRelayCert(dir string) (certFile, keyFile string, pool *x509.CertPool, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", nil, fmt.Errorf("serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", nil, fmt.Errorf("create cert: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		return "", "", nil, fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return "", "", nil, fmt.Errorf("write key: %w", err)
	}
	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return "", "", nil, errors.New("failed to pool generated cert")
	}
	return certFile, keyFile, pool, nil
}

// relaySubprocess is the config for a spawned local relay.
type relaySubprocess struct {
	bin      string // relay binary; empty = this executable
	addr     string // RELAY_ADDR
	certFile string
	keyFile  string
	cores    string // taskset CPU list (Linux); empty = unpinned
	gogc     int
}

// startRelaySubprocess launches `<bin> relay` with the given env, optionally
// pinned to cores via taskset (Linux only). It returns a stop func that
// terminates the relay gracefully (SIGTERM, then Kill after a grace period).
func startRelaySubprocess(ctx context.Context, cfg relaySubprocess) (func(), error) {
	bin := cfg.bin
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("locate self for --start-relay: %w", err)
		}
		bin = exe
	}
	name, args := bin, []string{"relay"}
	if cfg.cores != "" {
		if runtime.GOOS != "linux" {
			slog.Warn("--relay-cores ignored (taskset is Linux-only)", "goos", runtime.GOOS)
		} else if ts, err := exec.LookPath("taskset"); err != nil {
			slog.Warn("--relay-cores ignored (taskset not found)", "cores", cfg.cores)
		} else {
			name, args = ts, append([]string{"-c", cfg.cores, bin}, args...)
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(),
		"RELAY_ADDR="+cfg.addr,
		"CERT_FILE="+cfg.certFile,
		"KEY_FILE="+cfg.keyFile,
		"RELAY_NAME=loadgen-sweep",
		"GOGC="+strconv.Itoa(cfg.gogc),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start relay subprocess: %w", err)
	}
	slog.Info("loadgen started relay subprocess", "addr", cfg.addr, "pid", cmd.Process.Pid, "cores", cfg.cores, "gogc", cfg.gogc)

	stop := func() {
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		_ = cmd.Process.Signal(syscall.SIGTERM) // best-effort graceful stop
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	return stop, nil
}
