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
  -X github.com/qumo-dev/qumo/internal/version.version=$(git describe --tags --always) \
  -X github.com/qumo-dev/qumo/internal/version.commit=$(git rev-parse --short HEAD) \
  -X github.com/qumo-dev/qumo/internal/version.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
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

### 🎮 Demo (local scenarios)
- `mage demo:up` - Start relay (echo) + RTMP + RTSP origins (generates cert if missing) — `docker/docker-compose.demo.yml`
- `mage demo:push` - Start opt-in ffmpeg test-pattern pushers (RTMP → `/rtmp/demo`, RTSP → `/rtsp/demo`)
- `mage demo:down` - Stop the demo environment (pushers included)
- `mage demo:logs` - Tail demo logs
- `mage demo:ps` - List demo containers

### 🔧 Utilities
- `mage cert` - Generate a local-dev WebTransport cert. Prefers **mkcert** (browser-trusted, no pinning, no expiry churn) when on PATH; falls back to a 14-day self-signed cert that pins via `VITE_CERT_HASH`. Install mkcert via `brew install mkcert` / `winget install FiloSottile.mkcert`. Set `CERT_HOSTS=host[,host…]` to add extra SANs (mkcert path only) for LAN/hostname access.
- `mage hash` - Compute cert SHA-256 (used by the self-signed fallback path)

## Usage

From the repo root:
```bash
# Run mage tasks directly — mage auto-detects ./magefiles
mage <target>
# optional: mage -d ./magefiles <target>
```
