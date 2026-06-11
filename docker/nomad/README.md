# Nomad LocalResolver simulation

A real single-region Nomad cluster that exercises the **`LocalResolver`** path —
Nomad native service discovery — which the static-`PEERS` topology compose
(`docker-compose.static.yml`) never touches.

## What this verifies (and what it doesn't)

| Path | Mechanism | Covered here |
|---|---|---|
| Edge → local hubs (within a region) | `LocalResolver` → Nomad `/v1/service/qumo-relay` | ✅ **yes** |
| Hub → remote hubs (cross-region) | `RemoteResolver` → control-plane `/peers` | ❌ no — different mechanism, see below |

A **single Nomad cluster models a single region.** Edges run
`ResolvePeers(PeerQuery{Role:"hub"})` with no region filter — they connect to
*every* hub Nomad returns — which is correct only because, in production, each
region has its own Nomad. Hubs take no action on the local resolver, so within
one cluster only edge→hub connections form.

Cross-region hub↔hub is **not** Nomad — it is the `RemoteResolver` talking to the
qumo-deploy `/peers` hub registry, whose write/registration path is not built yet
(qumo-deploy#549). Verifying that needs one Nomad cluster *per region* plus a
populated `/peers`; it is out of scope for this sim.

> This is a manual simulation. There are **no automated integration tests** wired
> to it by design.

## Run

```bash
# 1. Build the relay image into the host Docker (Nomad's docker driver runs it).
docker build -f docker/Dockerfile -t qumo:local .

# 2. Bring up Nomad + submit the qumo-cluster job (2 hubs + 2 edges, region=asia).
docker compose -f docker/docker-compose.nomad.yml up -d

# 3. Nomad UI / API.
open http://localhost:4646/ui/jobs       # or just curl below
```

## Verify

All `nomad` commands below assume `export NOMAD_ADDR=http://localhost:4646`.

**1. The four relays registered, with the right role/region tags** (this is the
discovery data `LocalResolver` reads):

```bash
nomad job status qumo-cluster            # 2 hubs + 2 edges "running"
nomad service info qumo-relay            # 4 instances; tags role=hub|edge, region=asia
```

**2. Edges actually discovered and connected to both hubs.** Each edge should
reach `qumo_relay_peers_connected = 2`; hubs stay at `0` (by design):

```bash
# pick an edge allocation id from `nomad job status qumo-cluster`
EDGE=<edge-alloc-id>
nomad alloc exec "$EDGE" wget -qO- http://localhost:4433/metrics \
  | grep -E 'qumo_relay_peers_connected|qumo_relay_peer_dial_attempts'
```

Expected on an **edge**: `qumo_relay_peers_connected 2` and
`qumo_relay_peer_dial_attempts{...,result="ok"}` ≥ 2.
Expected on a **hub**: `qumo_relay_peers_connected 0`.

Give it ~`LOCAL_RESOLVER_INTERVAL` (5s) after the allocations are healthy.

## Tear down

```bash
nomad job stop -purge qumo-cluster                       # stop relay containers
docker compose -f docker/docker-compose.nomad.yml down   # stop Nomad + network
```

## Troubleshooting

This sim was authored without a Docker host to validate against; the Nomad↔Docker
networking is the most likely thing to need a small tweak. Common issues:

- **`Failed to find image qumo:local`** — run the `docker build ... -t qumo:local`
  step first; Nomad's docker driver pulls from the *host* Docker images.
- **Edges show `peers_connected 0`** — inspect the registered addresses:
  `nomad service info qumo-relay`. If the `Address` is the host IP or `127.0.0.1`
  rather than a `qumo-net` container IP, the relays can't dial each other.
  Fixes to try: confirm `address_mode = "driver"` on the service, that the task
  `network_mode = "qumo-net"` matches the compose network `name: qumo-net`, and
  that the agent's `network_interface = "eth0"` is the qumo-net interface.
- **`/var/run/docker.sock` not found / permission denied** — on Docker Desktop
  (macOS/Windows) ensure the socket is shared; the `nomad` service mounts it and
  runs `privileged: true` to drive sibling containers.
- **Relays can't reach `http://nomad:4646`** — the relay containers must be on
  `qumo-net` (they are, via `network_mode`); verify with
  `nomad alloc exec <id> wget -qO- http://nomad:4646/v1/agent/health`.
