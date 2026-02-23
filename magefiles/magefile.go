//go:build mage

package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Default target to run when none is specified
var Default = Help

// Help displays available mage targets
func Help() error {
	fmt.Println("📖 qumo - MoQT Relay & SDN Controller")
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
	fmt.Println("    mage fmt          - Format code with go fmt")
	fmt.Println("    mage vet          - Run go vet for static analysis")
	fmt.Println("    mage lint         - Run golangci-lint (if installed)")
	fmt.Println("    mage check        - Run fmt, vet, and test")
	fmt.Println()
	fmt.Println("  🚀 Runtime:")
	fmt.Println("    mage relay        - Start relay server")
	fmt.Println("    mage sdn          - Start SDN controller")
	fmt.Println("    mage dev          - Start relay + SDN in dev mode")
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
	fmt.Println()
	fmt.Println("  🎮 Demo:")
	fmt.Println("    mage demo:up      - Start demo environment (3 relays + SDN)")
	fmt.Println("    mage demo:setup   - Configure demo network topology")
	fmt.Println("    mage demo:down    - Stop demo environment")
	fmt.Println("    mage demo:status  - Check demo status")
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

	cmd := exec.Command("go", "build", "-o", "./bin/"+binaryName, ".")
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

	cmd := exec.Command("go", "install", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Installed: qumo")
	fmt.Println("   Run with: qumo relay -config config.relay.yaml")
	fmt.Println("            qumo sdn -config config.sdn.yaml")
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
	fmt.Println("   Config: ./config.relay.yaml")
	fmt.Println("   Certs: certs/server.crt, certs/server.key (run 'mage cert')")
	fmt.Println("   MoQT: https://localhost:4433")
	fmt.Println("   HTTP: http://localhost:8080")
	fmt.Println()

	cmd := exec.Command("go", "run", ".", "relay", "-config", "config.relay.yaml")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// SDN starts the SDN controller
func SDN() error {
	fmt.Println("🎛️  Starting SDN controller...")
	fmt.Println("   Config: ./config.sdn.yaml")
	fmt.Println("   HTTP: http://localhost:8090")
	fmt.Println()
	fmt.Println("   Available endpoints:")
	fmt.Println("     PUT/DELETE /relay/<name>       - Register/deregister relay")
	fmt.Println("     GET /route?from=A&to=B         - Query shortest path")
	fmt.Println("     GET /graph                     - Get topology graph")
	fmt.Println("     PUT/DELETE /announce/<relay>/<path> - Announce content")
	fmt.Println("     GET /announce/lookup?broadcast_path=X - Find content providers")
	fmt.Println("     GET /announce                  - List all announcements")
	fmt.Println()

	cmd := exec.Command("go", "run", ".", "sdn", "-config", "config.sdn.yaml")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Dev starts relay and SDN in development mode (parallel)
func Dev() error {
	fmt.Println("🚀 Starting development environment...")
	fmt.Println("   This will start both relay and SDN controller")
	fmt.Println("   Press Ctrl+C to stop all services")
	fmt.Println()

	// Note: This is a simple implementation that runs them sequentially.
	// For true parallel execution, users should run in separate terminals:
	//   Terminal 1: mage sdn
	//   Terminal 2: mage relay
	//   Terminal 3: mage web

	fmt.Println("💡 For better development experience, run in separate terminals:")
	fmt.Println("   Terminal 1: mage sdn")
	fmt.Println("   Terminal 2: mage relay")
	fmt.Println("   Terminal 3: mage web")
	fmt.Println()
	fmt.Println("Starting SDN controller...")

	return SDN()
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

	// Start Vite dev server in the solid-deno project
	webDir := "solid-deno"
	cmd := exec.Command("npm", "run", "dev")
	cmd.Dir = webDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// WebBuild builds the web demo for production
func WebBuild() error {
	fmt.Println("🔨 Building web demo...")

	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = "solid-deno"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// WebClean cleans web build artifacts
func WebClean() error {
	fmt.Println("🧹 Cleaning web artifacts...")
	return sh.Rm("solid-deno/dist")
}

// Cert generates TLS certificates using mkcert
func Cert() error {
	fmt.Println("🔐 Generating TLS certificates...")

	// Check if mkcert is installed
	if err := exec.Command("mkcert", "-version").Run(); err != nil {
		fmt.Println("❌ mkcert is not installed!")
		fmt.Println()
		fmt.Println("Please install mkcert:")
		fmt.Println("  Windows: winget install FiloSottile.mkcert")
		fmt.Println("  macOS:   brew install mkcert")
		fmt.Println("  Linux:   See https://github.com/FiloSottile/mkcert#installation")
		return fmt.Errorf("mkcert not found")
	}

	// Ensure certs directory exists
	if err := os.MkdirAll("certs", 0755); err != nil {
		return err
	}

	// Install local CA if not already installed
	fmt.Println("📦 Setting up local CA...")
	installCmd := exec.Command("mkcert", "-install")
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		fmt.Println("⚠️  Warning: Failed to install CA, continuing anyway...")
	}

	// Generate certificates for localhost
	fmt.Println("📝 Generating certificates for localhost...")
	certCmd := exec.Command("mkcert",
		"-cert-file", "certs/server.crt",
		"-key-file", "certs/server.key",
		"localhost", "127.0.0.1", "::1")
	certCmd.Stdout = os.Stdout
	certCmd.Stderr = os.Stderr
	if err := certCmd.Run(); err != nil {
		return fmt.Errorf("failed to generate certificates: %w", err)
	}

	// Compute SHA-256 of the generated certificate and write to certs/server.crt.sha256
	err := Hash()
	if err != nil {
		fmt.Println("⚠️  Warning: failed to compute cert hash:", err)
	}

	fmt.Println()
	fmt.Println("✅ Certificates generated successfully!")
	fmt.Println("   📄 certs/server.crt")
	fmt.Println("   🔑 certs/server.key")
	fmt.Println()
	fmt.Println("💡 These certificates are trusted by your system")
	fmt.Println("   You can now use WebTransport without certificate errors!")
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

	cmd := exec.Command("go", "build", "-o", "./bin/"+binaryName, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Built: bin/" + binaryName)
	fmt.Println("   Run with: ./bin/qumo relay -config config.relay.yaml")
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

	cmd := exec.Command("docker", "pull", "ghcr.io/okdaichi/qumo:latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Image pulled successfully!")
	fmt.Println("   Tag: ghcr.io/okdaichi/qumo:latest")
	return nil
}

// Build builds the Docker image
func (Docker) Build() error {
	fmt.Println("🐳 Building Docker image...")

	cmd := exec.Command("docker", "build", "-t", "qumo:latest", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Docker image built: qumo:latest")
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
	fmt.Println("   SDN Controller: http://localhost:8090")
	fmt.Println("   Relay Health:   http://localhost:8080/health")
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

// Demo provides demo environment commands
type Demo mg.Namespace

// Up starts the demo environment with 3 relays and SDN
func (Demo) Up() error {
	fmt.Println("🎮 Starting demo environment...")
	fmt.Println("   1 SDN Controller + 3 Relay Servers")
	fmt.Println("   (Tokyo, London, New York)")
	fmt.Println()
	fmt.Println("   Network topology will be auto-configured!")
	fmt.Println()

	cmd := exec.Command("docker", "compose", "-f", "docker-compose.simple.yml", "up")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Down stops the demo environment
func (Demo) Down() error {
	fmt.Println("🛑 Stopping demo environment...")

	cmd := exec.Command("docker", "compose", "-f", "docker-compose.simple.yml", "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Status shows the status of demo services
func (Demo) Status() error {
	fmt.Println("📊 Demo Environment Status:")
	fmt.Println()

	cmd := exec.Command("docker", "compose", "-f", "docker-compose.simple.yml", "ps")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("💡 Try these commands:")
	fmt.Println("   curl http://localhost:8090/graph | jq")
	fmt.Println("   curl \"http://localhost:8090/route?from=relay-tokyo&to=relay-newyork\"")
	fmt.Println("   curl http://localhost:8080/health  # Tokyo")
	fmt.Println("   curl http://localhost:8081/health  # London")
	fmt.Println("   curl http://localhost:8082/health  # New York")
	return nil
}
