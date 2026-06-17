# Nomad agent config layered on top of `-dev`.
#
# `-dev` already runs a combined server+client with the docker, exec and raw_exec
# drivers enabled and an in-memory state store. This file only adds what the
# qumo simulation needs:
#   - the docker driver may launch *sibling* containers through the mounted host
#     docker socket and attach them to the pre-existing `qumo-net` network
#     (the job sets `network_mode = "qumo-net"`).
#   - the client advertises its `eth0` address (on qumo-net) as the node IP.

plugin "docker" {
  config {
    allow_privileged = true

    volumes {
      enabled = true
    }
  }
}

client {
  # The container's interface on qumo-net. Keeps Nomad's node IP on the same
  # network the relays use, so service discovery addresses are reachable.
  network_interface = "eth0"
}
