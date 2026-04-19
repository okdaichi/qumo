# qumo

[![CI](https://github.com/okdaichi/qumo/actions/workflows/ci.yml/badge.svg)](https://github.com/okdaichi/qumo/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/okdaichi/qumo)](https://goreportcard.com/report/github.com/okdaichi/qumo)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**qumo** is a high-performance Media over QUIC (MoQ) relay server with intelligent topology management, enabling distributed media streaming over the QUIC transport protocol.

## Features

- 🚀 **High-Performance Relay**: Built on QUIC for low-latency media streaming
- 📡 **MoQT Protocol**: Full Media over QUIC Transport support (moq-lite draft-03)
- 🔗 **Peer-Based Topology**: Relays connect to each other via ANNOUNCE_PLEASE for decentralized content discovery
- 📊 **Observability**: Prometheus metrics, health probes, and status APIs
- 🔒 **TLS Security**: Built-in TLS 1.3 support for encrypted connections
- 🐳 **Docker-Support**: Env-var zero-config; prebuilt multi-arch images on GHCR (ghcr.io/okdaichi/qumo)

## Installation

#### Option 1: Install via Go

```bash
go install github.com/okdaichi/qumo@latest
```

#### Option 2: Download Binary

Download the latest binary from [GitHub Releases](https://github.com/okdaichi/qumo/releases):

```bash
# Linux/macOS
curl -L https://github.com/okdaichi/qumo/releases/latest/download/qumo-linux-amd64 -o qumo
chmod +x qumo
export ADVERTISE_ADDR=localhost:4433
export INSECURE=true
./qumo relay

# Windows: download qumo-windows-amd64.exe from the releases page
```

#### Option 3: Docker

See [docker/README.md](docker/README.md) for compose examples, GHCR usage, and deployment options.

#### Option 4: Build from Source

```bash
git clone https://github.com/okdaichi/qumo.git
cd qumo
mage build        # builds bin/qumo with version info
# or: go build -o qumo .
```

## Usage

```bash
qumo relay       # Start MoQ relay server (QUIC/MoQT, WebTransport, peer mesh)
qumo bootstrap   # Start bootstrap discovery server (HTTP peer registry)
qumo rtmp        # Start RTMP ingest server (bridges RTMP → MoQT)
qumo version     # Print build-time version info
```

For environment variables and configuration, see `relay-config.example.env` and `bootstrap-config.example.env`. For Docker-based deployment, see [docker/README.md](docker/README.md).

## Architecture

### System Overview

```mermaid
graph LR
    Publisher["Publisher<br/>(Browser/WebTransport)"]
    Bootstrap["Bootstrap Server<br/>(qumo bootstrap)"]
    Hub["Hub Relay<br/>(qumo relay)"]
    EdgeA["Edge Relay A<br/>(qumo relay)"]
    EdgeB["Edge Relay B<br/>(qumo relay)"]
    Subscriber["Subscriber<br/>(Browser/WebTransport)"]

    Publisher -->|"QUIC/MoQ<br/>WebTransport"| EdgeA
    EdgeA <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| Hub
    Hub <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| EdgeB
    EdgeB -->|"QUIC/MoQ<br/>WebTransport"| Subscriber

    EdgeA -->|"POST /register (heartbeat)<br/>GET /peers (discovery)"| Bootstrap
    Hub -->|"POST /register (heartbeat)<br/>GET /peers (discovery)"| Bootstrap
    EdgeB -->|"POST /register (heartbeat)<br/>GET /peers (discovery)"| Bootstrap
```

### Peer Discovery Lifecycle

```mermaid
graph TD
    Start["Relay Startup"] --> Static{"Static PEERS<br/>configured?"}
    Static -->|yes| DialStatic["Dial static peer<br/>(maintainPeer — goroutine)"]
    Static -->|no| BS{"BOOTSTRAP_URLS<br/>configured?"}
    DialStatic --> BS

    BS -->|yes| Register["POST /register<br/>(heartbeat every interval)"]
    Register --> Tick["Periodic tick<br/>(every BOOTSTRAP_INTERVAL)"]
    Tick --> Discover["GET /peers<br/>(role-aware query)"]
    Discover -->|"edge: local edges + local hub"| DialDynamic
    Discover -->|"hub: local peers + cross-region hub"| DialDynamic
    Discover -->|"default: local peers"| DialDynamic
    Discover --> Tick

    DialDynamic["Dial new peer<br/>(maintainPeer — goroutine,<br/>skip already-connected)"] --> ALPN["QUIC dial<br/>(ALPN: moqt)"]
    ALPN --> Announce["Send ANNOUNCE_PLEASE<br/>prefix='/'"]
    Announce --> Receive["Receive ANNOUNCE<br/>from peer"]
    Receive --> TrackMux["Register on local TrackMux"]
    TrackMux --> Serve["Subscribers can access<br/>remote content"]

    ALPN -->|"dial failed"| Retry["Wait 5s"]
    Serve -->|"connection lost"| Retry
    Retry --> ALPN

    BS -->|no| Idle["Accept incoming<br/>connections only"]
```

## Development

### Requirements

- **Go 1.26+** — [Download](https://golang.org/dl/) or use your package manager
- **Deno** (optional, for web demo) — [Download](https://deno.land/) — see [solid-deno/README.md](solid-deno/README.md) for setup
- **Mage** — Build automation tool
  ```bash
  go install github.com/magefile/mage@latest
  ```
  Then run `mage help` to see all available tasks.

For complete Mage documentation and all available targets, see [magefiles/README.md](magefiles/README.md).

### Project Structure

```
qumo/
├── docker/                     # Docker artifacts & docs
│   ├── Dockerfile              # Multi-stage container build
│   ├── docker-compose.yml               # Single relay (local build)
│   ├── docker-compose.external.yml      # Single relay (GHCR prebuilt)
│   ├── docker-compose.topology.yml      # Full 3-region topology (bootstrap + hub + edge)
│   └── README.md               # Docker usage guide
│
├── internal/                   # Core implementation
│   ├── cli/                    # CLI entrypoints & env-var config
│   ├── relay/                  # Relay server (handlers, peer connections, caching)
│   ├── bootstrap/              # Bootstrap server & client (peer discovery via HTTP)
│   ├── rtmp/                   # RTMP utilities
│   ├── ingest/                 # RTMP ingest & FLV parsing
│   └── version/                # Version info
│
├── magefiles/                  # Build automation (Mage tasks)
│
├── certs/                      # TLS certificate examples
├── benchmarks/                 # Performance benchmarks
├── examples/                   # Usage examples
├── .github/workflows/          # CI/CD pipelines
├── go.mod & go.sum             # Go dependencies
└── main.go                     # Entry point
```

### Build System (Mage)

See [magefiles/README.md](magefiles/README.md) for the complete reference. Common targets:

```bash
mage build         # Build binary to bin/qumo
mage test          # Run tests
mage check         # Format, vet, and test
mage lint          # Run golangci-lint
mage docker:build  # Build Docker image
mage relay         # Run relay server locally
mage smoke         # Run cross-region streaming smoke test
```

