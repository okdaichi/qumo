# Docker — qumo

This directory consolidates the project's Dockerfiles, compose manifests, and Docker-related usage.
All configuration is driven by **environment variables** — no config YAML files are needed at the repo root.

Files
- `Dockerfile` — image build used by CI (GHCR)
- `docker-compose.yml` — single relay (local build)
- `docker-compose.external.yml` — single relay (pre-built image)
- `docker-compose.topology.yml` — **full 3-region topology** (bootstrap + hub + edge per region)

Quick start (3-region topology)

```bash
# Start the full topology: 3 bootstraps + 3 hubs + 3 edges
docker compose -f docker/docker-compose.topology.yml up --build

# Check a bootstrap
curl http://localhost:8091/peers

# Check a relay
curl http://localhost:9001/health

# Stop
docker compose -f docker/docker-compose.topology.yml down
```

Run pre-built image (GHCR)

```bash
# Pull image
docker pull ghcr.io/qumo-dev/qumo:latest

# Run relay (config generated from env vars)
docker run -d \
  --name qumo-relay \
  -p 4433:4433/udp \
  -p 8080:4433 \
  -e INSECURE=true \
  -e RELAY_NAME=relay-1 \
  -e REGION=asia \
  -e ROLE=hub \
  ghcr.io/qumo-dev/qumo:latest relay
```

Environment variables (relay)

| Variable | Default | Description |
|---|---|---|
| `RELAY_ADDR` | `0.0.0.0:4433` | Bind address |
| `RELAY_NAME` | `relay-$HOSTNAME` | Node ID |
| `REGION` | (empty) | Region label |
| `ROLE` | (empty) | `hub` or `edge` |
| `ADVERTISE_ADDR` | (empty) | Public address for peers |
| `INSECURE` | `false` | Auto-generate self-signed certs |
| `BOOTSTRAP_URLS` | (empty) | Comma-separated bootstrap URLs |
| `BOOTSTRAP_INTERVAL` | `15s` | Bootstrap poll interval |
| `PEERS` | (empty) | Comma-separated static peer addresses |

Environment variables (bootstrap)

| Variable | Default | Description |
|---|---|---|
| `BOOTSTRAP_ADDR` | `:8080` | Bind address |
| `BOOTSTRAP_TTL` | `30s` | Node TTL before expiration |
| `BOOTSTRAP_MAX_PEERS` | `20` | Max peers returned per query |

Build locally

```bash
docker build -f docker/Dockerfile -t qumo:local .
```

Notes
- The container listens on port `4433` for QUIC (UDP) and also serves HTTP health/metrics on the same port (TCP).
- If you previously used `config.relay.yaml` at the repo root, configuration is now driven entirely by environment variables — the binary reads env vars directly.
