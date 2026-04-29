# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Concurrent group fill limiting (`internal/relay`):** A buffered-channel semaphore
  (`fillSem`) now bounds the number of in-flight fill goroutines per `trackDistributor`
  to `MaxConcurrentGroupFills` (default `max(32, 2×GOMAXPROCS)`). When all slots are
  occupied, `ingest` blocks on the semaphore rather than spawning unboundedly, providing
  natural backpressure against bursty or slow-consumer ingest. A new Prometheus gauge
  `qumo_relay_group_fills_inflight` exposes the current in-flight count for observability.
  `MaxConcurrentGroupFills` is a package-level variable and can be overridden before
  calling `Relay` for environment-specific tuning.

- **Concurrent frame filling in group cache (`internal/relay`):** `trackDistributor.ingest`
  now reserves a ring slot synchronously (preserving group ordering) and fills frames
  concurrently via a `sync.WaitGroup`-guarded goroutine per group. This prevents a slow
  upstream group from blocking the next `AcceptGroup` call and improves throughput under
  bursty or high-latency ingest conditions. A `frameSource` interface decouples the ring
  from `*moqt.GroupReader`, enabling deterministic unit tests without importing unexported
  upstream types. Frame pool buffers are now correctly returned via `defer ring.pool.Put`
  after each fill, eliminating a pool-leak under concurrent load.

- **Enhanced Prometheus metrics (`internal/relay`, `internal/ingest`):** Comprehensive
  observability for both relay and ingest subsystems.
  - *Relay metrics*: New gauges for `sessions_active`, `subscribers_active`,
    `peers_connected`, `broadcasts_active`, and `buffer_depth_groups`.
    Added `subscriber_skips_total` counter for QoS tracking and `subscribe_errors_total`,
    `peer_dial_attempts_total`, `route_replacements_total`, and `route_rejections_total`
    for operational analysis.
    Added node-level byte accounting for relay ingress and egress with
    `qumo_relay_ingress_bytes_total{node_id}` and `qumo_relay_egress_bytes_total{node_id}`.
  - *QUIC-layer metrics*: Added `conn_smoothed_rtt_ms` and `conn_packet_loss_rate`
    for native QUIC connections (skipped for WebTransport).
  - *Ingest metrics*: Achieved parity with relay by adding `publishers_active`,
    `subscribers_active`, `buffer_depth_groups`, and `subscriber_skips_total`.
  - *Latency Histograms*: Added `session_rtt_seconds` and `group_delivery_seconds`
    histograms to track RTT and delivery performance distributions.
  - *Session Polling*: Re-enabled RTT and estimated bitrate polling for all MoQT sessions
    (including WebTransport) via the new `pollSessionStats` background routine.
  - *Label Cleanup*: Dynamic Prometheus labels (remote addresses, track names) are now
    rigorously deleted on session/track termination to prevent memory growth.
- **Route selection improvements (`internal/relay`):**
  - `isBetterRoute` now returns a detailed `rejectionReason` when a route is rejected.
  - Rejections are logged and tracked via the `qumo_relay_route_rejections_total` metric.
- **Health check refinement (`internal/relay`):** `statusHandler` no longer tracks
  active connections manually; it now relies on Prometheus gauges for session counts.
- **Bootstrap API authentication (`internal/bootstrap`, `internal/cli`):** The `/register`
  and `/peers` endpoints now support optional bearer token authentication. Set
  `BOOTSTRAP_AUTH_TOKEN` on both the bootstrap server and relay nodes to require an
  `Authorization: Bearer <token>` header. When the variable is empty, authentication is
  skipped and existing behaviour is preserved (backward compatible).
- **mTLS support (`internal/bootstrap`, `internal/cli`):** Mutual TLS can now be enabled
  across the entire relay mesh by setting `CA_FILE` (PEM CA certificate).
  - *Relay server*: when `CA_FILE` is set, presented peer certificates are verified against
    the CA. By default client certificates are optional; set `MTLS_REQUIRED=true` to require
    a certificate on every connection.
  - *Relay dialer*: trusts only the CA pool and presents this node's `CERT_FILE` cert as a
    client certificate when dialing peer relays.
  - *Bootstrap server*: set `BOOTSTRAP_CERT_FILE` + `BOOTSTRAP_KEY_FILE` to enable HTTPS;
    additionally setting `CA_FILE` enables mTLS client verification on the bootstrap server.
  - *Bootstrap client*: `ClientConfig` gains a `TLSConfig *tls.Config` field; when `CA_FILE`
    is set on the relay, bootstrap HTTP clients automatically present the relay client cert
    and verify the bootstrap server against the CA pool.
  All changes are opt-in; leaving `CA_FILE` unset preserves existing behaviour.

- **`RouteStats` struct and `RouteReporter` interface (`internal/relay`):** Routing quality
  metrics (`Alive`, `Hops`, `Bitrate`, `RTT`) are now exposed per handler. `Alive` is
  derived from both the handler's child context and `Announcement.IsActive()`.
- **`Drainable` interface and `DrainTimeout` (`internal/relay`):** Displaced handlers are
  gracefully drained over a 30-second window before their upstream subscription is cancelled,
  allowing in-flight groups to finish delivery.
- **`isBetterRoute` route comparison (`internal/relay`):** Route selection is now explicit:
  a live route always beats a dead one; among live routes, fewer hops → higher bitrate → lower
  RTT decides the winner. The existing handler is kept unless the new candidate is strictly better.
- **`markConnected` / `markUnconnected` peer deduplication (`internal/relay`):** Server-wide
  address tracking prevents duplicate `maintainPeer` goroutines for the same peer. Static peers
  and bootstrap-discovered peers now share the same deduplication map. `markUnconnected` is
  called when a `maintainPeer` goroutine exits, restoring the address for future reconnection.
- **`context.AfterFunc` handler cleanup (`internal/relay`):** `handler.cancel` is registered
  via `context.AfterFunc(sess.Context(), ...)` in `Relay`, so the handler's child context is
  cancelled as soon as the upstream session closes.
- **`trackDistributor.ingest` context propagation (`internal/relay`):** `AcceptGroup` now
  receives the handler's child context instead of `context.Background()`, ensuring ingest
  goroutines stop promptly when the handler is drained or the session closes.
- **Streaming smoke test (`mage smoke`):** End-to-end smoke test that publishes
  test frames over MoQT and verifies all frames are received intact by a subscriber.
  Accepts `-pub` and `-sub` flags to target independent relay endpoints, enabling
  cross-region mesh validation. Exits with code 1 on frame loss or hash mismatch.
- **`internal/smoketest` package:** Smoke test implementation moved from `cmd/smoketest`
  to `internal/smoketest` and invoked via the Mage build system.
- **`docker-compose.topology.yml` port protocols:** UDP and TCP protocols are now
  explicitly declared for all relay service ports.

### Changed

- **Dependency upgrades and project-wide refactoring:**
  - Upgraded MoQ dependencies (Go `gomoqt` and JS/Deno `@qumo/moq`) to v0.15.0.
  - Migrated frontend MoQ dependency from `@okdaichi/moq` to `@qumo/moq`.
  - Updated all frontend import paths to use the new `@qumo/moq` package.
  - Upgraded frontend dependencies: `solid-js` to v1.9.12, `vite` to v7.3.2, `@types/node` to v25.6.0, and `vite-plugin-solid` to v2.11.12.
  - Refactored SVG assets (`vite.svg`, `solid.svg`) for improved formatting and readability.
- **Repository ownership transferred:** Project ownership moved from `okdaichi` personal account to the `qumo-dev` organization.
- **`discoverPeers` deduplication unified (`internal/relay`):** The per-`discoverPeers`
  local `map[string]struct{}` and its mutex have been removed. Deduplication is now handled
  server-wide by `markConnected`, keyed on peer address instead of peer ID.
- **`newRelayHandler` owns a cancellable child context (`internal/relay`):** The handler's
  `ctx` is no longer `sess.Context()` directly; it is a child created with
  `context.WithCancel`, giving `Drain` and `AfterFunc` cleanup independent control.
- **gomoqt upgraded to v0.13.4:** Tracks upstream moq-lite API changes including
  updated `moqt.Dialer` and session lifecycle improvements.
- **`relay.Server` fields made public:** `MOQServer` and `MOQDialer` are now exported
  fields, enabling callers to configure the underlying server and dialer directly.
- **Context propagation fixed:** `Subscribe` and `ReceiveAnnouncement` now use the
  session-scoped context (`h.ctx` / `sess.Context()`) instead of `context.Background()`,
  so upstream connections are cancelled when the relay session closes.
- **`statusHandler` nil-check restored:** `Server.init()` no longer overwrites a
  caller-supplied `statusHandler`.
- **Simplified relay health endpoint (`internal/relay`):** `/health` no longer supports
  probe query parameters or separate liveness/readiness semantics; it now returns a
  single unified health payload with `live: true` and `ready: true`.
- **TLS configuration hardened:** `InsecureSkipVerify` is now set only on the dialer
  TLS config when `INSECURE=true`; the server-side TLS config no longer carries it.
- **`Peer.Address` comment corrected:** Removed unsupported `https://` scheme from
  documentation; only `moqt://` and bare `host:port` are accepted by `DialQUIC`.

### Fixed

- **AVCC codec mismatch in web demo publisher (`solid-deno/src/publish/PublishBoard.tsx`,
  `solid-deno/src/subscribe/SubscribeBoard.tsx`):** `VideoEncoder` configured with `avc1.*`
  outputs AVCC-format frames, but the catalog was misreporting the codec as `avc3.*`
  (Annex-B) and discarding `decoderConfig.description`. The fix uses the MSF catalog
  `Track.initData` field (Base64-encoded `AVCDecoderConfigurationRecord`) so subscribers
  can configure `VideoDecoder` with the correct `description`. AVCC bytes are now forwarded
  as-is — no per-frame conversion.
- **`fs.Parse` error handling:** `RunRTMP` now propagates `flag.Parse` errors instead
  of silently discarding them (flag set changed to `ContinueOnError`).
- **Smoke test error handling:** `frame.Write` and `gw.Close` errors are now caught
  and logged during publishing; early return prevents sending corrupt groups.

### Security

- **G118 excluded (`internal/relay`):** `context.WithCancel` cancel function is stored
  in `relayHandler.cancel` and called later via `Drain` or `context.AfterFunc`; gosec cannot
  trace cross-function ownership so the finding is a false positive.
- **gosec integrated into golangci-lint:** Removed the standalone `securego/gosec`
  GitHub Actions step; gosec now runs as part of `golangci-lint` with SARIF output
  uploaded to GitHub Security. Rule exclusions are centrally managed in `.golangci.yml`
  with per-path scope and rationale comments, eliminating inline `#nosec` annotations.
- **G115 excluded globally:** Integer overflow conversions in RTMP/AMF3/QUIC protocol
  encoding are intentional truncations mandated by the respective wire formats.

## [v0.4.0] - 2026-04-15

### Breaking Changes

- **SDN controller removed:** `qumo sdn` subcommand and all SDN-related packages (`internal/sdn`,
  `internal/topology`) have been removed. Cross-relay content discovery is now handled natively
  by moq-lite draft-03's ANNOUNCE_PLEASE mechanism.
- **config.sdn.yaml removed:** No longer needed. Relay-to-relay connectivity is configured via
  `peers` in `config.relay.yaml`.
- **ALPN changed from `moq-00` to `moq-lite-03`:** Peers must be upgraded together; mixed
  deployments with older versions are not supported.

### Added

- **Peer-based announce relay:** Each relay can dial upstream peers (via `peers` config section).
  On connect, the relay sends `ANNOUNCE_PLEASE "/"` to receive all announcements, then registers
  them on the local `TrackMux`. Subscribers transparently access remote content without a central
  controller.
- **`relay.Config.Peers`:** New config field accepts a list of peer addresses in
  `moqt://host:port` or `https://host:port` form.
- **Auto-reconnect:** `ConnectPeers` maintains each peer connection with a 5-second retry loop,
  recovering from transient network failures.
- **`docker-entrypoint.sh` PEERS env var:** `PEERS=moqt://relay-b:4433,moqt://relay-c:4433`
  generates the `peers:` block in the relay config automatically.

### Changed

- **gomoqt upgraded to v0.12.1** (moq-lite draft-03): `moqt.Dialer` replaces the old client
  API; `Session.AcceptAnnounce` / `AnnouncementReader.Announcements` used for peer discovery.
- **`docker-compose.simple.yml` rewritten:** Now runs 3 peer-connected relay nodes instead of
  SDN + 3 relays. Node interconnection is via `PEERS` env var.
- **CI workflow fixed:** Build job updated to Go 1.26, correct binary path (`./bin/qumo`), and
  `qumo version` check. Codecov condition corrected to `1.26`.
- **Dockerfile fixed:** Removed `config.sdn.yaml` COPY (file deleted), corrected
  `docker-entrypoint.sh` path relative to build context, removed SDN port 8090.
- **NextProtos updated:** `setupTLS` now uses `moqt.NextProtoMOQ` constant (`"moq-lite-03"`)
  instead of a hardcoded `"moq-00"` string.
- `internal/relay/session.go` removed (empty `Session interface{}`).

### Fixed

- `TestIsVideoSequenceHeader`: `0x27 0x00` correctly returns `true` — codec ID is AVC and
  packet type is sequence header regardless of keyframe bit.
- `TestRelayHandler_ConcurrentSubscribe`: fixed `newTestRelayHandler` to construct handler
  directly, bypassing the nil-session guard added to `newRelayHandler`.

## [v0.3.1] - 2026-03-12

### Fixed

- **WebTransport connectivity (critical):** Upgrade `gomoqt` to v0.10.5, which calls
  `ConfigureHTTP3Server(wtserver.H3)` in `NewServer()`. Without this, `H3.ConnContext` was
  `nil` and `webtransport-go v0.10.0`'s `Upgrade()` could not retrieve the QUIC connection
  from the HTTP request context, returning `"webtransport: missing QUIC connection"` on every
  attempt. Browsers surfaced this as `ERR_METHOD_NOT_SUPPORTED`.
- **JS streaming pipeline:** Upgrade `@okdaichi/moq` to v0.10.5. `mux.publishFunc()` is now
  called before media capture starts, ensuring the relay has a track handler registered before
  any subscriber attempts to `SUBSCRIBE`. Previously the handler was registered after
  `sourceNode.start()`, so the relay never received track requests.
- **Video codec mismatch:** Subscriber no longer hardcodes VP9 decoder config. The publisher
  sends actual codec parameters via a `video.meta` MoQ track; the subscriber reconfigures
  `VideoDecoder` reactively via a SolidJS `createEffect`.
- **Subscriber deadlock:** `ServeTrack()` held `sync.RWMutex` while calling `subscribe()`,
  which performs a network round-trip. A second track's `ServeTrack` blocked on the same
  mutex, preventing video from ever appearing on the subscriber side.
- **Unhandled promise rejection on stop:** `SubscribeBoard` now catches errors from
  `session.subscribe()` gracefully. Previously, stopping a subscription while `SUBSCRIBE_OK`
  was in-flight caused `RESET_STREAM` errors to surface as unhandled promise rejections in the
  browser console.
- Relay `Server.Relay` method unexported to `relay` (internal API cleanup).
- Fix `mage dev` command to correctly start Vite dev server via Deno.

### Changed

- **`sync.Map` replaces `sync.RWMutex`:** `RelayHandler` track distributor map now uses
  `sync.Map` with `LoadOrStore` for lock-free concurrent access, eliminating the manual
  double-check locking pattern.
- **`newRelayHandler` constructor:** All `RelayHandler` creation sites (`server.go`,
  `remote_fetcher.go`, tests) unified through a single constructor function.
- **Log level audit:** Demoted high-frequency logs (`"group cached"`, `"Relaying track"`) to
  `Debug`; promoted error-like conditions to `Warn`; removed redundant `Info` logs. Added
  `"session established"` / `"session closed"` Info logs for connection lifecycle visibility.
- Relay error handling improved; session errors are logged rather than panicked.
- `.env.example` corrected: `VITE_RELAY_URL` must use `https://` (WebTransport requires TLS).

### Added

- **Regression tests:** `TestRelayHandler_ConcurrentSubscribe` (deadlock regression) and
  `TestRelayHandler_LoadOrStore` (sync.Map deduplication).

## [v0.3.0] - 2026-02-14

### Added

- Versioning system: embed `version`, `commit`, and `date` via `ldflags` at build time
  (`internal/version`).
- Topology: node TTL and automatic sweeper for stale node cleanup.
- Topology: heartbeat support and `trackedPath` for dynamic route re-computation.
- Docker: self-registration support; removed separate setup service.

## [v0.2.0] - 2026-02-14

### Added

- `RemoteFetcher`: cross-relay content routing so subscribers can pull tracks from peer relays.
- `PeerRegistry`: relay metadata management for federated deployments.
- SDN controller subcommand (`qumo sdn`) with HTTP API for topology management.
- Topology package: graph data structures, Dijkstra shortest-path algorithm, persistence, and
  HA synchronization.
- SDN announce system for content/path discovery.
- Docker Compose environments: simple single-node and external-user variants.
- Mage task: `mage docker` and related helpers for containerized development.

### Changed

- Upgrade `gomoqt` to v0.10.3.
- Remove legacy upstream cascading system; replace with `RemoteFetcher`.
- Restructure config files; remove admin module.

### Fixed

- Relay healthcheck: use TCP:4433 where HTTP server actually listens.
- SDN handler mount path.

## [v0.1.0] - 2026-01-05

### Added

- Initial relay server implementation using MoQ-over-WebTransport (`gomoqt`).
- `TrackMux`-based track distribution with group caching and frame pooling.
- SolidJS + Deno frontend (`solid-deno`) with `PublishBoard` and `SubscribeBoard`.
- User identity via randomly generated usernames.
- Basic Mage build automation.
- CI workflow with test coverage.

[Unreleased]: https://github.com/okdaichi/qumo/compare/v0.4.0...HEAD
[v0.4.0]: https://github.com/okdaichi/qumo/compare/v0.3.1...v0.4.0
[v0.3.1]: https://github.com/okdaichi/qumo/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/okdaichi/qumo/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/okdaichi/qumo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/okdaichi/qumo/releases/tag/v0.1.0
