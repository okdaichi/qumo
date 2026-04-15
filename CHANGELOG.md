# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Ingest: migrate to `msf.Broadcast`:** Track routing and catalog management now delegated to
  `msf.Broadcast` from gomoqt. Video/audio tracks registered via `RegisterTrack()` with MSF
  metadata; catalog track auto-served. Removed `catalog.go` (`msfCatalog`/`msfTrack`/
  `buildCatalogJSON`) and `publishCatalog()` helper.
- **Ingest: `context.Context` adoption:** `newIngestHandler` accepts parent context; `videoTrack`
  and `singleTrack` hold `context.Context` instead of `<-chan struct{}`; `trackBuffer.serve`
  takes `context.Context` as first argument.
- **Ingest: encapsulation improvements:** Added `serve()` methods to `videoTrack`/`singleTrack`
  to hide `trackBuffer` internals. Replaced `boolPtr`/`int64Ptr` helpers with Go 1.26
  `new(literal)` syntax.
- **API change:** `NewSession()` now returns `(*Session, error)`. `Session.PublishCatalog()`
  removed; replaced by `Session.RegisterVideo(*AVCConfig) error` and
  `Session.RegisterAudio(*AACConfig) error`.

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
