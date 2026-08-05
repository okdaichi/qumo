---
title: Peer topology
description: How qumo relays discover each other and mesh via ANNOUNCE_PLEASE.
weight: 2
---

## System overview

```mermaid
graph LR
    Publisher["Publisher<br/>(Browser/WebTransport)"]
    Hub["Hub Relay<br/>(qumo relay)"]
    EdgeA["Edge Relay A<br/>(qumo relay)"]
    EdgeB["Edge Relay B<br/>(qumo relay)"]
    Subscriber["Subscriber<br/>(Browser/WebTransport)"]

    Publisher -->|"QUIC/MoQ<br/>WebTransport"| EdgeA
    EdgeA <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| Hub
    Hub <-->|"ANNOUNCE_PLEASE<br/>QUIC peer"| EdgeB
    EdgeB -->|"QUIC/MoQ<br/>WebTransport"| Subscriber
```

A node's role (`--role hub`, `--role edge`, or unset for a flat/standalone
relay) is a CLI flag on `qumo relay` — see [CLI → relay]({{< relref "../cli/relay" >}}).

## Peer discovery

On startup, each relay discovers peers through one or more `PeerResolver`
implementations:

1. **Static peers** (`PEERS`) — dial each address directly and maintain the connection.
2. **Nomad native discovery** (within-cluster) — automatically discovers peers within
   the same Nomad cluster via the Nomad service API. Edges discover all local
   hubs; hubs discover nothing locally (no local hub↔hub connections). See
   [Nomad]({{< relref "nomad" >}}).
3. **Remote resolver** (cross-cluster, optional) — queries an external traffic
   resolver API (e.g. qumo-enterprise) for cross-cluster hub discovery. Hubs
   discover remote hubs; edges never query the remote resolver.

Each connection dials QUIC with ALPN `moqt`, exchanges `ANNOUNCE_PLEASE` /
`ANNOUNCE`, and registers the peer's tracks on the local `TrackMux`. On
disconnect the connection is retried after 5s.

```mermaid
graph TD
    Start["Relay Startup"]

    Start -->|"for each PEER"| ALPN
    Start -->|"Nomad API (within-cluster)"| Resolve["PeerResolver.ResolvePeers"]
    Start -->|"Remote resolver (cross-cluster)"| Resolve

    Resolve -->|"returned peer list"| ALPN

    ALPN["QUIC dial (ALPN: moq-lite-04)"] --> Announce["ANNOUNCE_PLEASE / ANNOUNCE"]
    Announce --> TrackMux["Register tracks on local TrackMux"]
    TrackMux --> Serve["Serve subscribers"]

    ALPN -->|"failed"| Retry["Wait 5s → retry"]
    Serve -->|"disconnected"| Retry
    Retry --> ALPN
```

## Route election

When more than one peer path can serve the same broadcast, the relay elects
one active route and keeps the losers as retained alternates, promoted if the
incumbent's announcement ends. This is visible via the
`qumo_relay_route_replacements_total`, `qumo_relay_routes_retained`, and
`qumo_relay_route_promotions_total` metrics — see
[Observability]({{< relref "../observability" >}}).

## Graceful migration

Route/subscription migration (make-before-break via route election) is the
primary mobility mechanism. `GOAWAY_REDIRECT_URI` is an escape-hatch: on
shutdown, it redirects clients/peers to a successor relay. See
[Configuration]({{< relref "../configuration" >}}#graceful-migration--goaway-optional).

## Related

- [Docker]({{< relref "docker" >}}) — `docker-compose.static.yml` wires a full 3-region topology with static `PEERS`.
- [Nomad]({{< relref "nomad" >}}) — exercises the Nomad-native `LocalResolver` path.
- [Configuration → Static peers]({{< relref "../configuration" >}}#static-peers) and [Local resolver]({{< relref "../configuration" >}}#local-resolver--nomad-native-discovery).
