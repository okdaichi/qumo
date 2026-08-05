---
title: Docker
description: Compose files for single-relay and multi-region qumo topologies.
weight: 1
---

All configuration is driven by **environment variables** — no config YAML
files are needed. The compose files live under `docker/` in the repo.

| File | Purpose |
|---|---|
| `Dockerfile` | Image build used by CI (published to GHCR) |
| `docker-compose.yml` | Single relay (local build) |
| `docker-compose.external.yml` | Single relay (pre-built GHCR image) |
| `docker-compose.static.yml` | Full 3-region topology (hub + edge per region), wired with static `PEERS` (no discovery) |
| `docker-compose.nomad.yml` + `nomad/` | Single-region Nomad cluster exercising `LocalResolver` — see [Nomad]({{< relref "nomad" >}}) |
| `docker-compose.demo.yml` | Local multi-scenario demo: relay (MoQ-MoQ echo) + RTMP + RTSP origins, with opt-in ffmpeg test-pattern pushers |

## Single relay

```bash
docker compose -f docker/docker-compose.yml up --build
curl http://localhost:4433/health
docker compose -f docker/docker-compose.yml down
```

## 3-region topology

```bash
docker compose -f docker/docker-compose.static.yml up --build
curl http://localhost:9001/health
docker compose -f docker/docker-compose.static.yml down
```

## Demo scenarios

Brings up the relay (MoQ-MoQ echo) plus RTMP and RTSP ingest origins together,
sharing one `mage cert` certificate. The RTMP/RTSP servers are standalone
origins — subscribers connect to the matching origin directly, not the relay.

```bash
mage demo:up      # relay + rtmp + rtsp (generates the cert if missing)
mage demo:push     # opt-in: push ffmpeg test patterns to /rtmp/demo, /rtsp/demo
mage demo:down     # stop everything, pushers included
```

| Scenario | Origin | Subscribe path | External push |
|---|---|---|---|
| Echo (MoQ-MoQ) | `https://localhost:4433` | publisher's path | n/a (publish from the demo) |
| RTMP ingest | `https://localhost:4443` | `/rtmp/demo` | RTMP → `localhost:1935/rtmp/demo` |
| RTSP ingest | `https://localhost:4543` | `/rtsp/demo` | RTSP → `localhost:8554/rtsp/demo` |

## Prebuilt image

```bash
docker pull ghcr.io/qumo-dev/qumo:latest

docker run -d \
  --name qumo-relay \
  -p 4433:4433/udp \
  -p 8080:4433 \
  -v "$PWD/certs:/app/certs:ro" \
  -e CERT_FILE=certs/server.crt \
  -e KEY_FILE=certs/server.key \
  -e RELAY_NAME=relay-1 \
  ghcr.io/qumo-dev/qumo:latest relay --role hub
```

## Build locally

```bash
docker build -f docker/Dockerfile -t qumo:local .
```

## Notes

- The container listens on port `4433` for QUIC (UDP) and also serves HTTP
  health/metrics on the same port (TCP).
- Configuration is driven entirely by environment variables — see
  [Configuration]({{< relref "../configuration" >}}) for the full reference.
