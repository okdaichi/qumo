# Docker — qumo

This directory consolidates the project's Dockerfiles, compose manifests, and Docker-related usage.
All configuration is driven by **environment variables** — no config YAML files are needed at the repo root.

Files
- `Dockerfile` — image build used by CI (GHCR)
- `docker-compose.yml` — single relay (local build)
- `docker-compose.external.yml` — single relay (pre-built image)
- `docker-compose.static.yml` — **full 3-region topology** (hub + edge per region), wired with **static `PEERS`** (no discovery)
- `docker-compose.nomad.yml` + `nomad/` — **real single-region Nomad cluster** that exercises the `LocalResolver` (Nomad service discovery) path; see [`nomad/README.md`](nomad/README.md)
- `docker-compose.demo.yml` — **local multi-scenario demo**: relay (MoQ-MoQ echo) + RTMP + RTSP origins up at once, with opt-in ffmpeg test-pattern pushers. Managed via `mage demo:up` / `mage demo:push` / `mage demo:down`

Quick start (single relay)

```bash
# Start a single relay
docker compose -f docker/docker-compose.yml up --build

# Check health
curl http://localhost:4433/health

# Stop
docker compose -f docker/docker-compose.yml down
```

Quick start (3-region topology)

```bash
# Start the full topology: 3 hubs + 3 edges
docker compose -f docker/docker-compose.static.yml up --build

# Check a relay health
curl http://localhost:9001/health

# Stop
docker compose -f docker/docker-compose.static.yml down
```

Quick start (demo scenarios)

Brings up the relay (MoQ-MoQ echo) plus RTMP and RTSP ingest origins together,
all sharing one `mage cert` cert so a single pinned `VITE_CERT_HASH` validates
every origin. The RTMP/RTSP servers are standalone origins — subscribers connect
to the matching origin directly (they do not dial the relay).

```bash
# 1. Bring up relay + rtmp + rtsp (generates the cert if missing)
mage demo:up

# 2. Opt-in: push ffmpeg test patterns to the RTMP/RTSP origins (→ /live/demo)
mage demo:push

# 3. Web demo: point it at the scenario's origin and run it
#    set VITE_RELAY_URL in solid-deno/.env, then:
mage web

# Stop everything (pushers included)
mage demo:down
```

Scenarios and their WebTransport origins (browser `VITE_RELAY_URL`):

| Scenario | Origin | Subscribe path | External push |
|---|---|---|---|
| Echo (MoQ-MoQ) | `https://localhost:4433` | publisher's path | n/a (publish from the demo) |
| RTMP ingest | `https://localhost:4443` | `/live/demo` | RTMP → `localhost:1935/live/demo` |
| RTSP ingest | `https://localhost:4543` | `/live/demo` | RTSP → `localhost:8554/live/demo` |

Until the in-demo scenario selector (#137) lands, switch origin by setting
`VITE_RELAY_URL` in `solid-deno/.env` and reloading the Vite dev server.

Run pre-built image (GHCR)

```bash
# Pull image
docker pull ghcr.io/qumo-dev/qumo:latest

# Run relay (config comes from env vars; mount a cert — e.g. from `mage cert`)
docker run -d \
  --name qumo-relay \
  -p 4433:4433/udp \
  -p 8080:4433 \
  -v "$PWD/certs:/app/certs:ro" \
  -e CERT_FILE=certs/server.crt \
  -e KEY_FILE=certs/server.key \
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
| `CERT_FILE` / `KEY_FILE` | `certs/server.crt` / `certs/server.key` | TLS cert/key (mount them; e.g. from `mage cert`) |
| `PEERS` | (empty) | Comma-separated static peer addresses |
| `LOCAL_RESOLVER_ADDR` | `http://localhost:4646` | Nomad HTTP API address |
| `LOCAL_RESOLVER_SERVICE_NAME` | `qumo-relay` | Nomad service name to query |
| `LOCAL_RESOLVER_INTERVAL` | `15s` | Local resolver poll interval |
| `REMOTE_RESOLVER_URL` | (empty) | Remote traffic resolver URL |
| `REMOTE_AUTH_TOKEN` | (empty) | Bearer token for remote resolver |
| `REMOTE_RESOLVE_INTERVAL` | `15s` | Remote resolver poll interval |
| `REMOTE_TLS_ENABLED` | `false` | Enable TLS for remote resolver |

Build locally

```bash
docker build -f docker/Dockerfile -t qumo:local .
```

Notes
- The container listens on port `4433` for QUIC (UDP) and also serves HTTP health/metrics on the same port (TCP).
- Configuration is driven entirely by environment variables — the binary reads env vars directly.
