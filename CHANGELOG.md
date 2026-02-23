# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
- Relay `Server.Relay` method unexported to `relay` (internal API cleanup).
- Fix `mage dev` command to correctly start Vite dev server via Deno.

### Changed

- Relay error handling improved; session errors are logged rather than panicked.
- `.env.example` corrected: `VITE_RELAY_URL` must use `https://` (WebTransport requires TLS).

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

[Unreleased]: https://github.com/okdaichi/qumo/compare/v0.3.0...HEAD
[v0.3.0]: https://github.com/okdaichi/qumo/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/okdaichi/qumo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/okdaichi/qumo/releases/tag/v0.1.0
