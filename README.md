# qumo

[![CI](https://github.com/qumo-dev/qumo/actions/workflows/ci.yml/badge.svg)](https://github.com/qumo-dev/qumo/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/qumo-dev/qumo)](https://goreportcard.com/report/github.com/qumo-dev/qumo)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**qumo** is a high-performance Media over QUIC (MoQ) relay server with intelligent topology management, enabling distributed media streaming over the QUIC transport protocol.

## Features

- 🚀 **High-Performance Relay**: Built on QUIC for low-latency media streaming
- 📡 **MoQT Protocol**: Full Media over QUIC Transport support (moq-lite draft-03)
- 🔗 **Peer-Based Topology**: Relays connect to each other via ANNOUNCE_PLEASE for decentralized content discovery
- 📊 **Observability**: Prometheus metrics, health probes, and status APIs
- 🔒 **TLS Security**: Built-in TLS 1.3 support for encrypted connections
- 🐳 **Docker-Support**: Env-var zero-config; prebuilt multi-arch images on GHCR (ghcr.io/qumo-dev/qumo)

## Installation

#### Option 1: Install via Go

```bash
go install github.com/qumo-dev/qumo@latest
```

#### Option 2: Download Binary

Download the latest binary from [GitHub Releases](https://github.com/qumo-dev/qumo/releases):

```bash
# Linux/macOS
curl -L https://github.com/qumo-dev/qumo/releases/latest/download/qumo-linux-amd64 -o qumo
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
git clone https://github.com/qumo-dev/qumo.git
cd qumo
mage build        # builds bin/qumo with version info
# or: go build -o qumo .
```

## Usage

```bash
qumo relay       # Start MoQ relay server (QUIC/MoQT, WebTransport, peer mesh)
qumo rtmp        # Start RTMP ingest server (bridges RTMP → MoQT)
qumo version     # Print build-time version info
```

For environment variables and configuration, see `relay-config.example.env`. For Docker-based deployment, see [docker/README.md](docker/README.md).

## Architecture

### System Overview

```mermaid
graph LR
    Publisher["Publisher<br/>(Browser/WebTransport)"]
    Hub["Hub Relay<br/>(qumo relay)"]
    EdgeA["Edge Relay A<br/>(qumo relay)"]
    EdgeB["Edge Relay B<br/>(qumo relay)"]
    Subscriber["Subscriber<br/>(Browser/WebTransport)"]

    Publisher -->|"QUIC/MoQ<br/>WebTransport"| EdgeA
    EdgeA <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| Hub
    Hub <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| EdgeB
    EdgeB -->|"QUIC/MoQ<br/>WebTransport"| Subscriber
```

### Peer Discovery

On startup, each relay discovers peers through one or more `PeerResolver` implementations:

1. **Static peers** (`PEERS`): dial each address directly and maintain the connection.
2. **Nomad native discovery** (within-cluster): automatically discovers peers within the same Nomad cluster via the Nomad service API. Edges discover all local hubs; hubs discover nothing locally (no local hub↔hub connections).
3. **Remote resolver** (cross-cluster, optional): queries an external traffic resolver API (e.g. qumo-enterprise) for cross-cluster hub discovery. Hubs discover remote hubs; edges never query the remote resolver.

Each connection dials QUIC with ALPN `moqt`, exchanges `ANNOUNCE_PLEASE` / `ANNOUNCE`, and registers the peer's tracks on the local `TrackMux`. On disconnect the connection is retried after 5 s.

```mermaid
graph TD
    Start["Relay Startup"]

    Start -->|"for each PEER"| ALPN
    Start -->|"Nomad API (within-cluster)"| Resolve["PeerResolver.ResolvePeers"]
    Start -->|"Remote resolver (cross-cluster)"| Resolve

    Resolve -->|"returned peer list"| ALPN

    ALPN["QUIC dial (ALPN: moqt)"] --> Announce["ANNOUNCE_PLEASE / ANNOUNCE"]
    Announce --> TrackMux["Register tracks on local TrackMux"]
    TrackMux --> Serve["Serve subscribers"]

    ALPN -->|"failed"| Retry["Wait 5s → retry"]
    Serve -->|"disconnected"| Retry
    Retry --> ALPN
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
│   ├── docker-compose.static.yml        # 3-region topology, static PEERS (no discovery)
│   ├── docker-compose.nomad.yml         # Single-region Nomad cluster (LocalResolver)
│   ├── nomad/                           # Nomad agent config + job spec
│   └── README.md               # Docker usage guide
│
├── internal/                   # Core implementation
│   ├── cli/                    # CLI entrypoints & env-var config
│   ├── relay/                  # Relay server (handlers, peer resolvers, caching)
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
