//go:build mage

package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

const versionPkg = "github.com/qumo-dev/qumo/internal/version"

// Default target to run when none is specified
var Default = Help

// versionLDFlags returns ldflags that embed version info into the binary.
func versionLDFlags() string {
	v := gitTag()
	c := gitCommit()
	d := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf("-s -w -X %s.version=%s -X %s.commit=%s -X %s.date=%s",
		versionPkg, v, versionPkg, c, versionPkg, d)
}

func gitTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}

func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "none"
	}
	return strings.TrimSpace(string(out))
}

// Help displays available mage targets
func Help() error {
	fmt.Println("📖 qumo - MoQT Relay")
	fmt.Printf("   Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()
	fmt.Println("Available targets:")
	fmt.Println()
	fmt.Println("  🔨 Build & Install:")
	fmt.Println("    mage build        - Build qumo binary")
	fmt.Println("    mage install      - Install qumo to $GOPATH/bin")
	fmt.Println("    mage clean        - Clean build artifacts")
	fmt.Println()
	fmt.Println("  🧪 Development:")
	fmt.Println("    mage test         - Run all tests")
	fmt.Println("    mage testVerbose  - Run tests with verbose output")
	fmt.Println("    mage coverage     - Run tests and write coverage.out")
	fmt.Println("    mage fmt          - Format code with go fmt")
	fmt.Println("    mage vet          - Run go vet for static analysis")
	fmt.Println("    mage lint         - Run golangci-lint (if installed)")
	fmt.Println("    mage check        - Run fmt, vet, and test")
	fmt.Println()
	fmt.Println("  🚀 Runtime:")
	fmt.Println("    mage relay        - Start relay server")
	fmt.Println("    mage dev          - Start relay in dev mode")
	fmt.Println()
	fmt.Println("  🌐 Web Demo:")
	fmt.Println("    mage web          - Start web demo (Vite dev server)")
	fmt.Println("    mage webBuild     - Build web demo for production")
	fmt.Println("    mage webClean     - Clean web build artifacts")
	fmt.Println()
	fmt.Println("  🏝️  Nomad Orchestration:")
	fmt.Println("    mage nomad:agent  - Start Nomad agent in dev mode")
	fmt.Println("    mage nomad:up     - Build and deploy to Nomad")
	fmt.Println("    mage nomad:stop   - Stop Nomad job")
	fmt.Println("    mage nomad:status - Show job status")
	fmt.Println("    mage nomad:logs   - Show job logs")
	fmt.Println("    mage nomad:clean  - Clean Nomad artifacts")
	fmt.Println()
	fmt.Println("  � Docker:")
	fmt.Println("    mage docker:pull    - Pull pre-built image from GHCR")
	fmt.Println("    mage docker:build   - Build Docker image")
	fmt.Println("    mage docker:up      - Start services with docker compose")
	fmt.Println("    mage docker:down  - Stop services")
	fmt.Println("    mage docker:logs  - View service logs")
	fmt.Println("    mage docker:ps    - List running containers")
	fmt.Println("    mage smoke      - Run cross-region streaming smoke test")
	fmt.Println()
	fmt.Println("  🎬 Demo (local scenarios):")
	fmt.Println("    mage demo:up      - relay + rtmp + rtsp origins (generates cert if missing)")
	fmt.Println("    mage demo:push    - opt-in ffmpeg test-pattern pushers (RTMP/RTSP → /live/demo)")
	fmt.Println("    mage demo:down    - stop the demo environment")
	fmt.Println("    mage demo:logs    - tail demo logs")
	fmt.Println("    mage demo:ps      - list demo containers")
	fmt.Println()

	fmt.Println("  �🔧 Utilities:")
	fmt.Println("    mage cert         - Generate TLS certificates using mkcert")
	fmt.Println("    mage hash         - Compute/write TLS cert SHA-256")
	fmt.Println()
	fmt.Println("  ℹ️  Info:")
	fmt.Println("    mage -l           - List all targets")
	fmt.Println("    mage help         - Show this help")
	fmt.Println()
	return nil
}

// Build builds the qumo binary
func Build() error {
	fmt.Println("🔨 Building qumo binary...")

	binaryName := "qumo"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	// Ensure build directory exists
	if err := os.MkdirAll("bin", 0755); err != nil {
		return err
	}

	ldflags := versionLDFlags()
	fmt.Printf("   version: %s\n", gitTag())

	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", "./bin/"+binaryName, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Built: bin/" + binaryName)
	return nil
}

// Install installs the qumo binary to $GOPATH/bin
func Install() error {
	fmt.Println("📦 Installing qumo to $GOPATH/bin...")

	cmd := exec.Command("go", "install", "-ldflags", versionLDFlags(), ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Installed: qumo")
	fmt.Println("   Configure relay with environment variables (see relay-config.example.env).")
	fmt.Println("   Run with: qumo relay")
	return nil
}

// Test runs all tests
func Test() error {
	fmt.Println("🧪 Running tests...")

	cmd := exec.Command("go", "test", "./...", "-count=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TestVerbose runs all tests with verbose output
func TestVerbose() error {
	fmt.Println("🧪 Running tests (verbose)...")

	cmd := exec.Command("go", "test", "./...", "-v", "-count=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Coverage runs tests and writes coverage report to coverage.out
func Coverage() error {
	fmt.Println("📊 Running tests with coverage...")

	cmd := exec.Command("go", "test", "-coverprofile=coverage.out", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ coverage report written to coverage.out")
	fmt.Println("Run 'go tool cover -html=coverage.out' to view the report locally")
	return nil
}

// Fmt formats all Go code
func Fmt() error {
	fmt.Println("✨ Formatting code...")

	cmd := exec.Command("go", "fmt", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Vet runs go vet for static analysis
func Vet() error {
	fmt.Println("🔍 Running go vet...")

	cmd := exec.Command("go", "vet", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Lint runs golangci-lint if installed
func Lint() error {
	fmt.Println("🔎 Running golangci-lint...")

	// Check if golangci-lint is installed
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		fmt.Println("⚠️  golangci-lint not found, skipping...")
		fmt.Println("   Install: https://golangci-lint.run/usage/install/")
		return nil
	}

	cmd := exec.Command("golangci-lint", "run", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Check runs fmt, vet, and test
func Check() error {
	fmt.Println("🔍 Running checks...")
	mg.Deps(Fmt, Vet, Test)
	fmt.Println("✅ All checks passed!")
	return nil
}

// Relay starts the qumo-relay server
func Relay() error {
	fmt.Println("📡 Starting qumo relay server...")
	fmt.Println("   Config: via Docker environment (see docker/docker-compose.static.yml)")
	fmt.Println("   Certs: certs/server.crt, certs/server.key (run 'mage cert')")
	fmt.Println("   Host: https://localhost:4433 (WebTransport/QUIC)")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Config is read from environment variables.
	// For local dev, set env vars or source relay-config.example.env.
	cmd := exec.CommandContext(ctx, "go", "run", ".", "relay")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// On Windows, exec.CommandContext only kills the direct process (go run),
	// not the compiled child binary it spawns. Kill the whole process tree.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return exec.Command("taskkill", "/F", "/T", "/PID",
				strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return cmd.Process.Kill()
	}

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil // cancelled by signal, not an error
	}
	return err
}

// Dev starts the relay in development mode.
func Dev() error {
	fmt.Println("🚀 Starting development environment...")
	fmt.Println("   Press Ctrl+C to stop")
	fmt.Println()

	fmt.Println("💡 For better development experience, run in separate terminals:")
	fmt.Println("   Terminal 1: mage relay")
	fmt.Println("   Terminal 2: mage web")
	fmt.Println()

	return Relay()
}

// Rtmp provides RTMP ingest commands.
type Rtmp mg.Namespace

// ... (existing Rtmp methods)

// Rtsp provides RTSP ingest commands.
type Rtsp mg.Namespace

// Serve starts the RTSP ingest server (RTSP → MoQT bridge).
func (Rtsp) Serve() error {
	fmt.Println("📡 Starting RTSP ingest server...")
	fmt.Println("   RTSP:   rtsp://localhost:8554/live/stream")
	fmt.Println("   MoQT:   https://localhost:4433 (WebTransport/QUIC)")
	fmt.Println()
	fmt.Println("💡 Push a stream with ffmpeg:")
	fmt.Println("   mage rtsp:stream")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	cmd := exec.CommandContext(ctx, "go", "run", ".", "rtsp")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return exec.Command("taskkill", "/F", "/T", "/PID",
				strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return cmd.Process.Kill()
	}

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Stream pushes a test stream via ffmpeg to the RTSP ingest server.
// Generates a 720p color-bar pattern with a 440 Hz sine tone.
//
// Environment variables:
//
//	RTSP_PATH=/live/demo  RTSP path          (default: /live/demo)
//	RTSP_ADDR=host:port                (default: localhost:8554)
func (Rtsp) Stream() error {
	path := envOrDefault("RTSP_PATH", "/live/demo")
	addr := envOrDefault("RTSP_ADDR", "localhost:8554")

	rtspURL := "rtsp://" + addr + path

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("❌ ffmpeg is not installed!")
		return fmt.Errorf("ffmpeg not found")
	}

	fmt.Println("🎬 Pushing test stream via ffmpeg...")
	fmt.Println("   RTSP URL:       ", rtspURL)
	fmt.Println("   Video:           1280x720 30fps (H.264 baseline)")
	fmt.Println("   Audio:           AAC 48kHz stereo")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-re",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-g", "60",
		"-c:a", "aac", "-ar", "48000", "-ac", "2",
		"-f", "rtsp", "-rtsp_transport", "tcp", rtspURL,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return exec.Command("taskkill", "/F", "/T", "/PID",
				strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return cmd.Process.Kill()
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// Demo prints instructions to run the full RTSP→MoQT demo pipeline.
func (Rtsp) Demo() {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    RTSP → MoQT Demo                        ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Run each command in a separate terminal:")
	fmt.Println()
	fmt.Println("  Terminal 1 — Start the RTSP→MoQT server:")
	fmt.Println("    $ mage rtsp:serve")
	fmt.Println()
	fmt.Println("  Terminal 2 — Start the web subscriber:")
	fmt.Println("    $ mage web")
	fmt.Println()
	fmt.Println("  Terminal 3 — Push a test stream via ffmpeg:")
	fmt.Println("    $ mage rtsp:stream")
	fmt.Println()
}

// Serve starts the RTMP ingest server (RTMP → MoQT bridge).
func (Rtmp) Serve() error {
	fmt.Println("📡 Starting RTMP ingest server...")
	fmt.Println("   Config: via -config flag")
	fmt.Println("   RTMP:   rtmp://localhost:1935/live/<stream-key>")
	fmt.Println("   MoQT:   https://localhost:4433 (WebTransport/QUIC)")
	fmt.Println()
	fmt.Println("💡 Push a stream with ffmpeg:")
	fmt.Println("   mage rtmp:stream")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	cmd := exec.CommandContext(ctx, "go", "run", ".", "rtmp", "-config", "config.rtmp.yaml")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return exec.Command("taskkill", "/F", "/T", "/PID",
				strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return cmd.Process.Kill()
	}

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Stream pushes a test stream via ffmpeg to the RTMP ingest server.
// Generates a 720p color-bar pattern with a 440 Hz sine tone.
//
// Environment variables:
//
//	APP=live       RTMP application name (default: live)
//	KEY=demo       Stream key           (default: demo)
//	RTMP_ADDR=host:port                (default: localhost:1935)
func (Rtmp) Stream() error {
	app := envOrDefault("APP", "live")
	key := envOrDefault("KEY", "demo")
	addr := envOrDefault("RTMP_ADDR", "localhost:1935")

	broadcastPath := "/" + app + "/" + key
	rtmpURL := "rtmp://" + addr + "/" + app + "/" + key

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("❌ ffmpeg is not installed!")
		fmt.Println()
		fmt.Println("Please install ffmpeg:")
		fmt.Println("  Windows: winget install Gyan.FFmpeg")
		fmt.Println("  macOS:   brew install ffmpeg")
		fmt.Println("  Linux:   apt install ffmpeg")
		return fmt.Errorf("ffmpeg not found")
	}

	fmt.Println("🎬 Pushing test stream via ffmpeg...")
	fmt.Println("   RTMP URL:       ", rtmpURL)
	fmt.Println("   Broadcast Path: ", broadcastPath)
	fmt.Println("   Tracks:          catalog, video, audio")
	fmt.Println("   Video:           1280x720 30fps (H.264 baseline)")
	fmt.Println("   Audio:           AAC 48kHz stereo")
	fmt.Println()
	fmt.Println("📺 To watch in browser:")
	fmt.Println("   1. Open http://localhost:5173")
	fmt.Println("   2. Set Broadcast Path to:", broadcastPath)
	fmt.Println("   3. Click 'Start Subscribing'")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-re",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-g", "60",
		"-c:a", "aac", "-ar", "48000", "-ac", "2",
		"-f", "flv", rtmpURL,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			return exec.Command("taskkill", "/F", "/T", "/PID",
				strconv.Itoa(cmd.Process.Pid)).Run()
		}
		return cmd.Process.Kill()
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// Demo prints instructions to run the full RTMP→MoQT demo pipeline.
func (Rtmp) Demo() {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    RTMP → MoQT Demo                        ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Run each command in a separate terminal:")
	fmt.Println()
	fmt.Println("  Terminal 1 — Start the RTMP→MoQT server:")
	fmt.Println("    $ mage rtmp:serve")
	fmt.Println()
	fmt.Println("  Terminal 2 — Start the web subscriber:")
	fmt.Println("    $ mage web")
	fmt.Println()
	fmt.Println("  Terminal 3 — Push a test stream via ffmpeg:")
	fmt.Println("    $ mage rtmp:stream")
	fmt.Println()
	fmt.Println("  Then open http://localhost:5173 in your browser,")
	fmt.Println("  set Broadcast Path to /live/demo, and click 'Start Subscribing'.")
	fmt.Println()
	fmt.Println("┌──────────────────────────────────────────────────────────────┐")
	fmt.Println("│  ffmpeg ──RTMP──▶ qumo (:1935)                              │")
	fmt.Println("│                    │                                         │")
	fmt.Println("│                    ▼                                         │")
	fmt.Println("│                  MoQT/QUIC (:4433)                           │")
	fmt.Println("│                    │                                         │")
	fmt.Println("│                    ▼                                         │")
	fmt.Println("│              Browser (:5173)                                 │")
	fmt.Println("│         /live/demo → catalog, video, audio                   │")
	fmt.Println("└──────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("💡 Custom stream key:  APP=myapp KEY=mystream mage rtmp:stream")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Web starts the web demo application (Vite dev server only)
// Note: Start relay separately with `./bin/qumo-relay` or `mage relay`
func Web() error {
	fmt.Println("🌐 Starting web demo...")
	fmt.Println("   Web Demo: http://localhost:5173")
	fmt.Println()
	fmt.Println("⚠️  Make sure relay is running separately:")
	fmt.Println("   ./bin/qumo-relay  # or: mage relay")
	fmt.Println()

	// Start Vite dev server in the playground project
	webDir := "playground"
	cmd := exec.Command("deno", "task", "dev")
	cmd.Dir = webDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// WebBuild builds the web demo for production
func WebBuild() error {
	fmt.Println("🔨 Building web demo...")

	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = "playground"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// WebClean cleans web build artifacts
func WebClean() error {
	fmt.Println("🧹 Cleaning web artifacts...")
	return sh.Rm("playground/dist")
}

// Cert generates a short-lived self-signed ECDSA certificate for WebTransport development.
// Chrome's serverCertificateHashes requires the certificate validity to be ≤14 days.
// The SHA-256 fingerprint is automatically written to playground/.env as VITE_CERT_HASH.
func Cert() error {
	fmt.Println("🔐 Generating WebTransport-compatible TLS certificate...")

	if err := os.MkdirAll("certs", 0755); err != nil {
		return err
	}

	// Generate ECDSA P-256 key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate key: %w", err)
	}

	// Self-signed certificate, valid for 14 days (Chrome WebTransport limit)
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(14 * 24 * time.Hour)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{Organization: []string{"qumo dev"}},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate PEM
	certFile, err := os.Create(filepath.Join("certs", "server.crt"))
	if err != nil {
		return err
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	// Write key PEM
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyFile, err := os.Create(filepath.Join("certs", "server.key"))
	if err != nil {
		return err
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return err
	}

	// Compute SHA-256 fingerprint and write to playground/.env
	fingerprint := sha256.Sum256(certDER)
	hexStr := hex.EncodeToString(fingerprint[:])

	if err := writeCertHashToEnv(hexStr); err != nil {
		fmt.Println("⚠️  Warning: failed to write cert hash to .env:", err)
	}

	fmt.Println()
	fmt.Println("✅ Certificate generated (valid 14 days)!")
	fmt.Printf("   📄 certs/server.crt  (expires %s)\n", notAfter.Format("2006-01-02"))
	fmt.Println("   🔑 certs/server.key")
	fmt.Println("   🔐 VITE_CERT_HASH written to playground/.env")
	fmt.Println()
	fmt.Println("💡 Re-run 'mage cert' when the certificate expires")
	return nil
}

// computeCertHash reads the PEM certificate at certs/server.crt, computes
// the SHA-256 hex fingerprint and returns it as a lower-case hex string.
func computeCertHash() (string, error) {
	b, err := os.ReadFile("certs/server.crt")
	if err != nil {
		return "", fmt.Errorf("failed to read cert: %w", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}
	sha := sha256.Sum256(cert.Raw)
	hexStr := hex.EncodeToString(sha[:])
	return hexStr, nil
}

// writeCertHashToEnv writes or updates the VITE_CERT_HASH entry in playground/.env.
// Other existing entries in the file are preserved.
func writeCertHashToEnv(hash string) error {
	envPath := filepath.Join("playground", ".env")
	lines := []string{}
	found := false

	if data, err := os.ReadFile(envPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VITE_CERT_HASH=") {
				lines = append(lines, "VITE_CERT_HASH="+hash)
				found = true
			} else {
				lines = append(lines, line)
			}
		}
	}

	if !found {
		if len(lines) == 0 {
			// Start from .env.example if .env doesn't exist
			if tpl, err := os.ReadFile(filepath.Join("playground", ".env.example")); err == nil {
				lines = strings.Split(string(tpl), "\n")
			}
		}
		lines = append(lines, "", "# Certificate hash for WebTransport (auto-generated by mage cert)")
		lines = append(lines, "VITE_CERT_HASH="+hash)
	}

	content := strings.Join(lines, "\n")
	return os.WriteFile(envPath, []byte(content), 0644)
}

// copyToClipboard attempts to copy the provided text to the system clipboard
// using platform-appropriate utilities. Returns an error if the required
// clipboard tool is not available or if the copy fails.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Use clip.exe via cmd to avoid issues
		cmd = exec.Command("cmd", "/c", "clip")
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		// Try wl-copy (Wayland), then xclip, then xsel
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard utility found (install wl-clipboard, xclip, or xsel)")
		}
	}

	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := in.Write([]byte(text)); err != nil {
		_ = in.Close()
		return err
	}
	_ = in.Close()
	return cmd.Wait()
}

// Hash computes (or re-computes) the certificate SHA-256 hash and prints the
// result. Optionally copies it to the system clipboard when run interactively.
func Hash() error {
	hexStr, err := computeCertHash()
	if err != nil {
		return err
	}
	fmt.Println("-----------🔐 CERT HASH-------------")
	fmt.Println("")
	fmt.Println(hexStr)
	fmt.Println("")
	fmt.Println("------------------------------------")

	// If stdin is not a TTY, avoid prompting and skip copying
	fi, _ := os.Stdin.Stat()
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		fmt.Println("Non-interactive stdin detected; skipping clipboard copy. Run 'mage hash' interactively to copy the hash to the clipboard.")
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Do you want to copy this hash to the clipboard? (y/n): ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if len(input) > 0 && (input[0] == 'y' || input[0] == 'Y') {
		if err := copyToClipboard(hexStr); err != nil {
			return fmt.Errorf("failed to copy to clipboard: %w", err)
		}
		fmt.Println("🔐 Copied cert hash to clipboard")
	} else {
		fmt.Println("Skipping clipboard copy.")
	}

	return nil
}

// Clean removes build artifacts
func Clean() error {
	fmt.Println("🧹 Cleaning build artifacts...")

	if err := sh.Rm("bin"); err != nil {
		fmt.Println("⚠️  No bin directory to clean")
	} else {
		fmt.Println("   Removed: bin/")
	}

	fmt.Println("✅ Cleanup complete!")
	return nil
}

// Nomad provides Nomad-specific commands
type Nomad mg.Namespace

// Agent starts the Nomad agent in dev mode
func (Nomad) Agent() error {
	fmt.Println("🏃 Starting Nomad Agent (Dev Mode)...")
	fmt.Println("   Access UI at http://localhost:4646")

	cmd := exec.Command("nomad", "agent", "-dev")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Build builds the Go binary for Nomad
func (Nomad) Build() error {
	fmt.Println("🔨 Building qumo binary for Nomad deployment...")

	binaryName := "qumo"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	// Ensure build directory exists
	if err := os.MkdirAll("bin", 0755); err != nil {
		return err
	}

	cmd := exec.Command("go", "build", "-ldflags", versionLDFlags(), "-o", "./bin/"+binaryName, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Built: bin/" + binaryName)
	fmt.Println("   Run with: ./bin/qumo relay -config <your-config.yaml>")
	return nil
}

// Up builds and deploys the job to Nomad
func (Nomad) Up() error {
	mg.Deps(Nomad.Build)

	fmt.Println("🚀 Submitting Job to Nomad...")
	cmd := exec.Command("nomad", "job", "run", "moq-relay.nomad")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Stop stops the Nomad job
func (Nomad) Stop() error {
	fmt.Println("🛑 Stopping Nomad job...")
	cmd := exec.Command("nomad", "job", "stop", "moq-relay")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Status shows the Nomad job status
func (Nomad) Status() error {
	fmt.Println("📊 Job Status:")
	cmd := exec.Command("nomad", "job", "status", "moq-relay")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Logs shows the Nomad job logs
func (Nomad) Logs() error {
	fmt.Println("📋 Job Logs:")
	cmd := exec.Command("nomad", "alloc", "logs", "-job", "moq-relay", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Clean removes Nomad artifacts
func (Nomad) Clean() error {
	fmt.Println("🧹 Cleaning Nomad artifacts...")
	// Optionally stop the job first
	_ = Nomad{}.Stop()
	time.Sleep(1 * time.Second)
	return sh.Rm("bin")
}

// Docker provides Docker-specific commands
type Docker mg.Namespace

// Pull pulls the latest image from GitHub Container Registry
func (Docker) Pull() error {
	fmt.Println("🐳 Pulling latest qumo image from GitHub Container Registry...")

	cmd := exec.Command("docker", "pull", "ghcr.io/qumo-dev/qumo:latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Image pulled successfully!")
	fmt.Println("   Tag: ghcr.io/qumo-dev/qumo:latest")
	return nil
}

// Build builds the Docker image with version metadata
func (Docker) Build() error {
	v := gitTag()
	c := gitCommit()
	d := time.Now().UTC().Format(time.RFC3339)
	tag := "qumo:" + strings.TrimPrefix(v, "v")

	fmt.Printf("🐳 Building Docker image %s ...\n", tag)

	cmd := exec.Command("docker", "build",
		"--build-arg", "VERSION="+v,
		"--build-arg", "COMMIT="+c,
		"--build-arg", "BUILD_DATE="+d,
		"-t", tag,
		"-t", "qumo:latest",
		".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Printf("✅ Docker image built: %s, qumo:latest\n", tag)
	return nil
}

// Up starts all services with docker compose
func (Docker) Up() error {
	fmt.Println("🚀 Starting services with docker compose...")

	cmd := exec.Command("docker", "compose", "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("✅ Services started!")
	fmt.Println("   Relay Health:   http://localhost:4433/health")
	fmt.Println()
	fmt.Println("💡 View logs: mage docker:logs")
	return nil
}

// Down stops all services
func (Docker) Down() error {
	fmt.Println("🛑 Stopping services...")

	cmd := exec.Command("docker", "compose", "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Logs shows service logs
func (Docker) Logs() error {
	fmt.Println("📋 Service Logs:")

	cmd := exec.Command("docker", "compose", "logs", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Ps lists running containers
func (Docker) Ps() error {
	fmt.Println("📦 Running Containers:")

	cmd := exec.Command("docker", "compose", "ps")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Restart restarts all services
func (Docker) Restart() error {
	fmt.Println("🔄 Restarting services...")

	cmd := exec.Command("docker", "compose", "restart")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// demoComposeFile is the manifest for the local multi-scenario demo environment.
const demoComposeFile = "docker/docker-compose.demo.yml"

// Demo provides commands for the local multi-scenario demo environment, which
// brings up the relay (echo) and the RTMP/RTSP ingest origins together so every
// demo pipeline is testable without reconfiguring. See docker/docker-compose.demo.yml.
type Demo mg.Namespace

// Up starts the demo environment: relay (MoQ-MoQ echo) + RTMP + RTSP origins.
// It generates the WebTransport cert via Cert() if missing (Cert also writes
// VITE_CERT_HASH into playground/.env). The ffmpeg test-pattern pushers are
// opt-in — see Push.
func (Demo) Up() error {
	if err := ensureDemoCert(); err != nil {
		return err
	}

	fmt.Println("🚀 Starting demo environment (relay + rtmp + rtsp)...")

	cmd := exec.Command("docker", "compose", "-f", demoComposeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("✅ Demo environment started!")
	fmt.Println("   WebTransport origins:")
	fmt.Println("     echo (relay): https://localhost:4433")
	fmt.Println("     rtmp:         https://localhost:4443   (push RTMP → localhost:1935/live/demo)")
	fmt.Println("     rtsp:         https://localhost:4543   (announce RTSP → localhost:8554/live/demo)")
	fmt.Println()
	fmt.Println("   RTMP/RTSP subscribe path: /live/demo")
	fmt.Println()
	fmt.Println("💡 Browser: set VITE_RELAY_URL in playground/.env to the origin you want,")
	fmt.Println("            then run `mage web`. (Scenario selector lands in #137.)")
	fmt.Println("💡 Push test streams: mage demo:push")
	fmt.Println("📋 Logs:             mage demo:logs")
	return nil
}

// Push starts the opt-in ffmpeg test-pattern pushers for the RTMP/RTSP scenarios
// (compose profile: push). Requires the ingest origins to be up (Demo.Up).
func (Demo) Push() error {
	fmt.Println("🎬 Starting test-pattern pushers (RTMP + RTSP → /live/demo)...")
	cmd := exec.Command("docker", "compose", "-f", demoComposeFile, "--profile", "push", "up", "-d", "rtmp-push", "rtsp-push")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("✅ Pushers started. Subscribe at /live/demo on the rtmp/rtsp origins.")
	return nil
}

// Down stops the demo environment and any running pushers.
func (Demo) Down() error {
	fmt.Println("🛑 Stopping demo environment...")
	cmd := exec.Command("docker", "compose", "-f", demoComposeFile, "--profile", "push", "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Logs tails demo service logs.
func (Demo) Logs() error {
	fmt.Println("📋 Demo logs:")
	cmd := exec.Command("docker", "compose", "-f", demoComposeFile, "logs", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Ps lists running demo containers.
func (Demo) Ps() error {
	fmt.Println("📦 Demo containers:")
	cmd := exec.Command("docker", "compose", "-f", demoComposeFile, "ps")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ensureDemoCert generates the WebTransport cert via Cert() only when missing,
// so `mage demo:up` does not churn the cert (and VITE_CERT_HASH) on every run.
func ensureDemoCert() error {
	if _, err := os.Stat("certs/server.crt"); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	fmt.Println("🔐 certs/server.crt not found — generating (mage cert)...")
	return Cert()
}

// Smoke runs a cross-region streaming smoke test against the topology.
// Smoke runs a streaming smoke test that publishes to one relay and subscribes from another.
// Defaults: pub=moqt://localhost:9002, sub=moqt://localhost:9006.
func Smoke(pub *string, sub *string) error { // pub: publisher relay URL, sub: subscriber relay URL
	pubURL := "moqt://localhost:9002"
	if pub != nil {
		pubURL = *pub
	}
	subURL := "moqt://localhost:9006"
	if sub != nil {
		subURL = *sub
	}

	fmt.Println("💨 Running streaming smoke test...")
	fmt.Printf("   Publish:   %s\n", pubURL)
	fmt.Printf("   Subscribe: %s\n", subURL)
	fmt.Println()

	cmd := exec.Command("go", "run", "./internal/smoketest",
		"-pub", pubURL,
		"-sub", subURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
