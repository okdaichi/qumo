# qumo-relay

Media over QUIC (MoQ) relay server implementation.

## Features

- **High-performance relay**: Optimized broadcast channel pattern for low-latency streaming
- **Group caching**: Configurable ring buffer for late subscribers
- **Frame pooling**: Memory-efficient frame allocation with sync.Pool
- **Graceful shutdown**: Proper signal handling and cleanup

## Installation

```bash
cd cmd/qumo-relay
go build
```

## Usage

### Basic Usage

```bash
./qumo-relay -config ../../configs/config.yaml
```

### Configuration

The server requires a YAML configuration file. See `configs/config.example.yaml` for all options:

```yaml
server:
  address: "0.0.0.0:4433"
  tls:
    cert_file: "certs/server.crt"
    key_file: "certs/server.key"

relay:
  FrameCapacity: 1500    # Frame buffer size (bytes)
  MaxGroupCache: 100     # Number of groups to cache
```

### TLS Certificates

The relay server requires TLS certificates for QUIC connections. 

**Recommended: Use mkcert for development**

[mkcert](https://github.com/FiloSottile/mkcert) automatically creates locally-trusted development certificates:

```bash
# Install mkcert (one-time setup)
# Windows (scoop)
scoop bucket add extras
scoop install mkcert

# macOS
brew install mkcert

# Linux - see https://github.com/FiloSottile/mkcert#installation

# Install local CA (one-time setup)
mkcert -install

# Generate certificates (from project root)
cd ../..  # Go to project root
mkdir -p certs
mkcert -cert-file certs/server.crt -key-file certs/server.key localhost 127.0.0.1 ::1
```

**Alternative: OpenSSL (for testing only)**

```bash
# Create certs directory
mkdir -p certs

# Generate self-signed certificate (valid for 365 days)
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key -out certs/server.crt \
  -days 365 -nodes -subj "/CN=localhost"
```

For production, use certificates from a trusted CA.

### Command-line Flags

- `-config <path>`: Path to configuration file (default: `configs/config.yaml`)

## Architecture

The relay server uses the following components from the `relay` package:

- **trackDistributor**: Manages multiple subscribers with broadcast channel pattern
- **groupRing**: Ring buffer for caching recent groups
- **FramePool**: Memory pool for efficient frame allocation

See [relay/README.md](../../relay/README.md) for detailed architecture documentation.

## Performance

Based on benchmarks with 100 subscribers:

- **Latency**: ~1.5µs per broadcast
- **CPU Usage**: ~1% (optimized with 1ms timeout)
- **Memory**: ~3.5MB/s with frame pooling

## Examples

### Running the Server

1. Generate TLS certificates (see above)
2. Copy and modify configuration:
   ```bash
   cp configs/config.example.yaml configs/config.yaml
   # Edit configs/config.yaml as needed
   ```
3. Start the server:
   ```bash
   ./qumo-relay
   ```

### Connecting Clients

Clients can connect using any MoQT-compatible client library. The server accepts connections on the configured address (default: `0.0.0.0:4433`).

## Troubleshooting

### "Failed to setup TLS"

Ensure certificate files exist and are valid:
```bash
openssl x509 -in certs/server.crt -text -noout
```

### "Address already in use"

Change the `server.address` in your config file or ensure no other process is using port 4433.

## Development

### Testing

Run relay package tests:
```bash
cd ../../relay
go test -v
```

### Debugging

Enable debug logging in `configs/config.yaml`:
```yaml
logging:
  level: "debug"
```

## License

See [LICENSE](../../LICENSE) for details.
