# Docker — qumo

This directory consolidates the project's Dockerfiles, compose manifests, and Docker-related usage.

Files
- `Dockerfile` — image build used by CI (GHCR)
- `docker-entrypoint.sh` — entrypoint used in the image
- `docker-compose.yml` — local build + compose example
- `docker-compose.external.yml` — compose for external deployment
- `docker-compose.simple.yml` — demo environment (SDN + 3 relays)

Quick start (demo)

```bash
# Start demo (SDN + 3 relays)
docker compose -f docker/docker-compose.simple.yml up

# View topology
curl http://localhost:8090/graph | jq

# Stop demo
docker compose -f docker/docker-compose.simple.yml down
```

Run pre-built image (GHCR)

```bash
# Pull image
docker pull ghcr.io/okdaichi/qumo:latest

# Run relay
docker run -d \
  --name qumo-relay \
  -p 4433:4433/udp \
  -p 8080:4433 \
  -v $(pwd)/certs:/app/certs:ro \
  ghcr.io/okdaichi/qumo:latest relay -config config.relay.yaml
```

Build locally

```bash
# Build using relocated Dockerfile (build context must be repo root)
docker build -f docker/Dockerfile -t qumo:local .
```

CI / GHCR

- GitHub Actions (release) builds & pushes multi-arch images to `ghcr.io/okdaichi/qumo`.
- The release workflow has been updated to use `docker/Dockerfile`.

Notes
- The container listens on port `4433` for QUIC (UDP) and also serves HTTP health/metrics on the same port (TCP). Demo compose files map host ports `8080/8081/8082` to container `4433` for convenience.
- If you previously used `docker-compose*.yml` at the repo root, use the files in `docker/` going forward.
