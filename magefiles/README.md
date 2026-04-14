# Magefiles (isolated)

This directory contains the project's Mage tasks and a dedicated `go.mod` so Mage's dependencies are isolated from the main module.

## Quick Start

Run mage tasks from the repo root:
```bash
# Build the binary
mage build

# Run tests
mage test

# Start relay server
mage relay

# Start web demo (in another terminal)
mage web
```

## Available Targets

Run `mage help` or `mage -l` to see all available targets.

### 🔨 Build & Install
- `mage build` - Build qumo binary
- `mage install` - Install to $GOPATH/bin
- `mage clean` - Clean build artifacts

Manual build with version info (useful if you don't run `mage`):

```bash
go build -ldflags "-s -w \
  -X github.com/okdaichi/qumo/internal/version.version=$(git describe --tags --always) \
  -X github.com/okdaichi/qumo/internal/version.commit=$(git rev-parse --short HEAD) \
  -X github.com/okdaichi/qumo/internal/version.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o qumo .
```

(Recommended: use `mage build` — it injects the same version metadata automatically.)

### 🧪 Development
- `mage test` - Run all tests
- `mage testVerbose` - Run tests with verbose output
- `mage coverage` - Run tests and write `coverage.out`
- `mage fmt` - Format code
- `mage vet` - Run static analysis
- `mage lint` - Run golangci-lint
- `mage check` - Run fmt, vet, and test

### 🚀 Runtime
- `mage relay` - Start relay server
- `mage dev` - Development mode info

### 🌐 Web Demo
- `mage web` - Start Vite dev server
- `mage webBuild` - Build for production
- `mage webClean` - Clean build artifacts

### 🐳 Docker
- `mage docker:pull` - Pull pre-built image from GHCR
- `mage docker:build` - Build Docker image (uses `docker/Dockerfile`)
- `mage docker:up` - Start services with docker compose
- `mage docker:down` - Stop services
- `mage docker:logs` - View service logs
- `mage docker:ps` - List running containers
- `mage docker:restart` - Restart services

> **Note:** Docker files (Dockerfile, compose manifests, etc.) are located in the `docker/` directory. See `docker/README.md` for manual Docker usage and examples.

### 🎮 Demo
- `mage demo:up` - Start demo environment (3 peer-connected relays) — uses `docker/docker-compose.simple.yml`
- `mage demo:down` - Stop demo environment
- `mage demo:status` - Check demo status

### 🔧 Utilities
- `mage cert` - Generate TLS certificates
- `mage hash` - Compute cert hash

## Usage

From the repo root:
```bash
# Run mage tasks directly — mage auto-detects ./magefiles
mage <target>
# optional: mage -d ./magefiles <target>
```
