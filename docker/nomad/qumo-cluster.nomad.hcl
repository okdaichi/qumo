# Single-region (asia) qumo cluster on Nomad.
#
# Purpose: exercise the LocalResolver (Nomad native service discovery) path that
# the static-PEERS topology compose never touches. Edges discover the local hubs
# via Nomad and dial them; hubs do nothing on the local resolver (by design —
# see internal/relay/server.go). This is the within-cluster (intra-region) path.
#
# Cross-region hub<->hub is NOT covered here: that is the RemoteResolver / control
# plane /peers path, which is a different mechanism (see docker/nomad/README.md).
#
# Both groups register the SAME service name "qumo-relay" with different role
# tags — exactly what LocalResolver filters on (PeerQuery{Role:"hub"}).

job "qumo-cluster" {
  datacenters = ["dc1"]
  type        = "service"

  # ───────────────────────────── Hubs ─────────────────────────────
  group "hubs" {
    count = 2

    network {
      # Service registration port. With address_mode = "driver" on the task
      # service below, the service registers as <container-ip-on-qumo-net>:4433.
      port "moqt" {
        to = 4433
      }
    }

    task "relay" {
      driver = "docker"

      config {
        image        = "qumo:local" # build first: docker build -f docker/Dockerfile -t qumo:local .
        network_mode = "qumo-net"   # share the compose network with edges + Nomad
        ports        = ["moqt"]
        # --role is a CLI flag, not an env var (see `qumo relay --help`); the
        # hub/edge branch in server.go's peer-discovery loop keys off this.
        args = ["relay", "--role", "hub"]
      }

      # Task-level service (required for address_mode = "driver").
      service {
        name         = "qumo-relay"
        port         = "moqt"
        address_mode = "driver" # register the container's qumo-net IP, not the host
        tags         = ["role=hub", "region=asia"]
      }

      env {
        INSECURE   = "true"
        RELAY_ADDR = "0.0.0.0:4433"
        RELAY_NAME = "hub-asia-${NOMAD_ALLOC_INDEX}"
        # Hubs point at the local resolver too, though they take no local action.
        LOCAL_RESOLVER_ADDR         = "http://nomad:4646"
        LOCAL_RESOLVER_SERVICE_NAME = "qumo-relay"
        LOCAL_RESOLVER_INTERVAL     = "5s"
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }

  # ───────────────────────────── Edges ─────────────────────────────
  group "edges" {
    count = 2

    network {
      port "moqt" {
        to = 4433
      }
    }

    task "relay" {
      driver = "docker"

      config {
        image        = "qumo:local"
        network_mode = "qumo-net"
        ports        = ["moqt"]
        args         = ["relay", "--role", "edge"]
      }

      service {
        name         = "qumo-relay"
        port         = "moqt"
        address_mode = "driver"
        tags         = ["role=edge", "region=asia"]
      }

      env {
        INSECURE   = "true"
        RELAY_ADDR = "0.0.0.0:4433"
        RELAY_NAME = "edge-asia-${NOMAD_ALLOC_INDEX}"
        # The path under test: edge -> LocalResolver -> Nomad -> all local hubs.
        LOCAL_RESOLVER_ADDR         = "http://nomad:4646"
        LOCAL_RESOLVER_SERVICE_NAME = "qumo-relay"
        LOCAL_RESOLVER_INTERVAL     = "5s"
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }
}
