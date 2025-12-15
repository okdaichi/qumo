# qumo

[![CI](https://github.com/okdaichi/qumo/actions/workflows/ci.yml/badge.svg)](https://github.com/okdaichi/qumo/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/okdaichi/qumo)](https://goreportcard.com/report/github.com/okdaichi/qumo)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**qumo** is a Media over QUIC (MoQ) relay server and CDN implementation, providing high-performance media streaming over the QUIC transport protocol.

## Features

- 🚀 High-performance media relay using QUIC
- 📡 Support for Media over QUIC protocol
- 🔒 Built-in TLS/security support
- 📊 Prometheus metrics for monitoring
- ⚙️ Flexible YAML-based configuration
- 🐳 Docker support (coming soon)

## Quick Start

### Prerequisites

- Go 1.21 or higher
- Basic understanding of QUIC protocol

### Installation

```bash
go install github.com/okdaichi/qumo/cmd/qumo-relay@latest
```

### Building from Source

```bash
git clone https://github.com/okdaichi/qumo.git
cd qumo
go build -o bin/qumo-relay ./cmd/qumo-relay
```

### Running the Relay

```bash
# Copy the example configuration
cp configs/config.example.yaml config.yaml

# Edit the configuration as needed
# vim config.yaml

# Run the relay server
./bin/qumo-relay --config config.yaml
```

## Configuration

See [`configs/config.example.yaml`](configs/config.example.yaml) for a complete configuration example with detailed comments.

Basic configuration structure:

```yaml
server:
  address: "0.0.0.0:4433"
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"

relay:
  upstream_url: ""           # Optional upstream server
  group_cache_size: 100      # Number of groups to cache
  frame_capacity: 1500       # Frame buffer size in bytes
```

## Architecture

qumo implements a high-performance MOQT relay using:

- **Frame Pool**: Zero-allocation frame reuse for optimal memory efficiency
- **Group Cache**: Ring buffer-based caching with configurable size
- **Broadcast Pattern**: Efficient subscriber notification with buffered channels
- **Concurrent Safety**: Comprehensive mutex protection and atomic operations

### Performance Optimizations

- Frame pooling reduces GC pressure
- Optimized 1ms notification timeout (based on benchmarks)
- Lock-free operations where possible
- Efficient ring buffer for group caching

## Project Structure

```
qumo/
├── cmd/
│   └── qumo-relay/        # Main relay server application
├── relay/                 # Core relay implementation
│   ├── server.go          # MOQT server wrapper
│   ├── handler.go         # Track relay handler
│   ├── frame_pool.go      # Memory-efficient frame pooling
│   ├── group_cache.go     # Ring buffer group cache
│   └── config.go          # Configuration structures
├── configs/               # Configuration examples
├── certs/                 # TLS certificates
├── docs/                  # Documentation
└── README.md
```

## Testing

qumo has comprehensive test coverage:

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run with race detector
go test -race ./...
```

Test coverage:
- **relay**: 32.7% statement coverage with 67 test cases
- **cmd/qumo-relay**: 42.9% statement coverage with 12 test cases
- Focus on concurrent operations, edge cases, and performance

## Documentation

- [Contributing Guidelines](CONTRIBUTING.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Configuration Guide](configs/config.example.yaml)

## Performance

Key performance characteristics:

- **Low Latency**: 1ms notification timeout for optimal balance
- **Memory Efficient**: Frame pooling prevents allocation overhead
- **Scalable**: Tested with 1000+ concurrent subscribers
- **Concurrent**: Thread-safe operations with minimal lock contention

Benchmarks (see `relay/*_test.go`):
- Frame pool operations: ~0 allocations per Get/Put cycle
- Broadcast to 1000 subscribers: <1ms
- Group cache operations: Constant-time access

## Development

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...

# Run tests with race detector
go test -race ./...
```

### Linting

```bash
golangci-lint run
```

### Contributing

We welcome contributions! Please see our [Contributing Guidelines](CONTRIBUTING.md) for details.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Community

- [Discussions](https://github.com/okdaichi/qumo/discussions) - Ask questions and discuss ideas
- [Issues](https://github.com/okdaichi/qumo/issues) - Report bugs and request features

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Media over QUIC (MoQ) Working Group
- IETF QUIC Working Group

## Status

⚠️ **This project is under active development.** APIs and features may change.
