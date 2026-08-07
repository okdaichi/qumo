---
title: Nomad
description: Nomad-native peer discovery — relays find their local-cluster peers via the Nomad service catalog instead of static PEERS.
weight: 3
---

Instead of hand-listing `PEERS`, relays running inside a Nomad cluster can
discover their local-cluster peers through Nomad's own service catalog. This
is scoped to **within one cluster** (one region); cross-region hub↔hub
discovery uses the separate remote resolver instead (see
[Configuration → Remote traffic resolver]({{< relref "../configuration" >}}#remote-traffic-resolver-optional)).

## How it works

- Each relay registers itself as a Nomad service (via your job spec's
  `service` block), tagged with its role.
- **Edges** poll the service catalog for peers tagged `role=hub` and connect
  to *all* of them — this is correct as long as each region runs its own
  Nomad cluster, since an edge has no way to filter by region within one
  cluster's catalog.
- **Hubs** take no action on the local resolver — they don't connect to other
  local hubs (cross-region hub↔hub is the remote resolver's job, not
  Nomad's).

## Configuration

```
LOCAL_RESOLVER_ADDR=http://nomad.service.consul:4646   # default: http://localhost:4646
LOCAL_RESOLVER_SERVICE_NAME=qumo-relay                  # default: qumo-relay
LOCAL_RESOLVER_INTERVAL=15s                             # default: 15s
```

See [Configuration → Local resolver]({{< relref "../configuration" >}}#local-resolver--nomad-native-discovery)
for the full reference. `--role hub`/`--role edge` is a CLI flag on
`qumo relay`, not an env var — see [CLI → relay]({{< relref "../cli/relay" >}}).

## Job spec

Register each relay under the same service name, tagged by role, so
`LOCAL_RESOLVER_SERVICE_NAME` resolves both:

```hcl
service {
  name         = "qumo-relay"
  port         = "moqt"
  address_mode = "driver" # register the container's actual reachable IP
  tags         = ["role=hub"]  # or "role=edge"
}
```

`LocalResolver` filters strictly on the `role=` tag — any other tags are
ignored by the relay itself.

## Verify

```bash
nomad service info qumo-relay
```

On an **edge**, `qumo_relay_peers_connected` (from `/metrics`) should reach
the number of hubs registered; on a **hub**, it should stay `0` — hubs take
no local action by design. Give it ~`LOCAL_RESOLVER_INTERVAL` after the
allocations report healthy.
