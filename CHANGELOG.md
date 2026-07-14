# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Fan-out collapse at K≥8 (`internal/relay`):** `DefaultGroupCacheSize` 8 → 128 (the 16ms ring window at 500fps was too small — scheduler jitter at high fan-out evicted groups before subscribers read them); `NotifyTimeout` 1ms → 5ms (80% fewer timer fires — the notify channel provides instant wake-up); `slog.Warn` → `slog.Debug` in the "fell behind; skipping" hot path (eliminated the loss→log→more-loss feedback loop). Combined, these push the fan-out knee from K≈8 to significantly higher on a 2-core runner.
- **Subscriber egress teardown hang (`internal/relay`, #286):** `trackDistributor.egress` now routes every non-delivery loop path through a single wait/cancellation `select` (on `twCtx.Done()`/`d.done`). Its fell-behind skip and cache-miss paths previously iterated via bare `continue` without consulting those signals, so a subscriber that fell behind could blind-spin past cancellation and never return when the subscriber disconnected or the relay shut down. That pinned gomoqt's stream-handler `WaitGroup`, so `Session.CloseWithError`'s `wg.Wait()` hung, the connection was never removed from the connManager, and `Server.Shutdown`/`Close` hung on `<-connManager.Done()` — the multi-subscriber teardown hang and churn-time goroutine leak. The cancellation signal already reached qumo (gomoqt's per-conn `goAway` force-closes the connection on ctx expiry, cancelling the subscribe-stream context `twCtx` derives from); qumo only needed to converge on the one select it already had. The per-group delivery body is extracted into `deliverGroup`. No gomoqt change required.

### Changed

- **Session-end handler cleanup moved to `newRelayHandler` (`internal/relay`):** the `context.AfterFunc(sess.Context(), cancel)` registration moved out of `installRoute` (where it needed a nil-session-context guard for test fixtures) into `newRelayHandler`, where the session is guaranteed non-nil. `installRoute` no longer touches `session.Context()`. No behavior change — every production handler still gets the cleanup exactly once via `newRelayHandler`.

### Added

- **Automation-friendly relay-chain benchmark suite (`internal/relay`, `scripts`, #284):** The relay-chain benchmarks emit machine-readable JSONL results (`BENCH_RESULTS_DIR`), including a 7-number latency summary (min/p25/median/p75/p95/p99/max) per config so the report can draw distribution plots. The fan-out sweep honors a `FANOUT_KS` env override. A new `TestRelayChain_ReconnectStorm` characterizes goroutine/heap behavior under subscriber churn (runs in the bench workflow via `RUN_STORM=1`, skipped in the per-PR CI gate). A zero-dependency Deno/TS report generator (`scripts/relay_bench_report.ts`) turns the JSONL into CSV + SVG plots: line charts with least-squares regression fits (per-hop latency slope, fan-out latency trend with R²), box-and-whisker plots of the latency distribution per K, a 4-panel overview (latency·loss·throughput·heap vs K), and a `derived.csv` of decision-grade numbers (per-hop ms/hop slope, fan-out knee K). One workflow runs it: `bench-relay.yml` (nightly full sweep K=1..128 + load + object-size + soak, plus an on-demand `workflow_dispatch` with a 30m/1h/3h/6h soak-duration choice). The per-PR CI integration gate skips the heavy `TestRelayChain_*` durability tests (`-skip='RelayChain'`); they run in the relay-bench workflow, their intended home.
- **Route recovery on incumbent-end (`internal/relay`, #279):** A route-election loser is now retained as a per-`BroadcastPath` alternate instead of being cancelled, and is promoted to the active route when the incumbent's announcement ends. This fixes the publisher-mobility failure mode where a candidate rejected during the overlap was permanently discarded, leaving the path stranded once the incumbent was retracted. Promotion fires only on a definitive announcement-end (asynchronously, since `Announcement.end()` runs callbacks inline), so it introduces no route oscillation. At most one alternate is retained per path, kept by route quality (`isBetterRoute`) rather than recency, and promotion is serialized with route election under a single lock so a promotion can never clobber a freshly-elected route. New metrics: `qumo_relay_routes_retained`, `qumo_relay_route_promotions_total`. The robust fix for autonomous split-brain (two live publications coexisting without coordination) remains a future generation/epoch fence.
- **Graceful migration / GOAWAY escape hatch (`internal/relay`, #280):** Wired `MOQDialer.OnGoaway` so a GOAWAY from an upstream peer relay is observed (`qumo_relay_peer_goaway_received_total{redirect}`) and logged instead of silently dropped, and plumbed `MOQServer.NextSessionURI` from the `GOAWAY_REDIRECT_URI` env so graceful shutdown advertises a redirect. GOAWAY is intentionally a session-level graceful-shutdown primitive (gomoqt exposes it as `Server.NextSessionURI` on `Shutdown` and `Dialer.OnGoaway`), which is what this wiring uses; publication relocation is handled by route/subscription migration (#279), not by GOAWAY.

## [v0.4.0] - 2026-07-08

### Breaking Changes

- **Relay `ROLE` env var removed -> `--role` flag:** the node topology role is now `qumo relay --role hub|edge` (flag-only, no env fallback). Deployments setting `ROLE=...` must switch to the flag. Secrets and deployment config remain env vars.
- **SDN controller removed:** `qumo sdn` subcommand and all SDN-related packages (`internal/sdn`,
  `internal/topology`) have been removed. Cross-relay content discovery is now handled natively
  by moq-lite draft-03's ANNOUNCE_PLEASE mechanism.
- **config.sdn.yaml removed:** No longer needed. Relay-to-relay connectivity is configured via
  `peers` in `config.relay.yaml`.
- **ALPN changed from `moq-00` to `moq-lite-03`:** Peers must be upgraded together; mixed
  deployments with older versions are not supported.


### Added

- **`RTSPServer.Addr()` (`internal/ingest`):** exposes the bound listener address (nil before `ListenAndServe` binds), so callers and tests that configure `Addr: ":0"` can learn the actual port without reaching into unexported state.
- **Playground UI refinement — visual polish, UX, RTSP camera pull (`playground`):** Refined dark/light palettes with elevation tokens (`--shadow-sm/md`), card hover shadows, backdrop-blur stats overlay, smoother transitions. Scenario tabs renamed for clarity: "Echo" → "Webcam" (browser camera/screen), "Camera" → "IP Camera" (RTSP pull), "RTSP" → "RTSP Push"; each has a one-line description below the picker. New "IP Camera" scenario with a camera-URL input form that starts/stops an in-process RTSP pull client (`POST /api/pull`, `/api/pull/stop`, `/api/pull/status` on the playground server) and serves MoQT on `:4543`. The subscribe board only renders once the pull is active; before that a guided empty-state placeholder is shown. The pull's WebTransport dial is deferred until the pull is active (no spurious ERR_CONNECTION_RESET on page load).
- **Demo logging via `@okdaichi/media-log` (`playground`):** The playground now consumes the external `@okdaichi/media-log` library (jsr) instead of bare `console.*`. All call sites in `cert.ts`, `publish/media.ts`, `PublishBoard.tsx` (7), and `SubscribeBoard.tsx` (13) move to tagged, structured, level-filtered loggers (`createLogger`/`createMediaLogger` with `MediaTags`); errors are passed as structured fields (serialized in `exportLogs()` for bug reports) instead of positional args, and the `[Publish]`/`[Subscribe]` string prefixes become tags. The encode (publish) and decode (subscribe) frame loops also feed media meters — `meter.fps`/`meter.bitrate` and, on subscribe, `meter.gauge` for RTT and decode-queue depth — so the pipeline emits one diagnostic fps/bitrate/rtt/queue line per second alongside the existing UI overlay. Requires `@okdaichi/media-log@^0.1.0` on jsr.
- **RTSP pull ingest — connect IP cameras directly (`internal/rtsp`, `internal/ingest`):** `qumo rtsp <url> [path]` dials an RTSP source (DESCRIBE/SETUP/PLAY), receives interleaved RTP, depacketizes H.264/AAC, and republishes as MoQT — so an IP camera feeds MoQ natively without an ffmpeg bridge. Supports Basic + Digest auth (credentials in the URL), TCP-interleaved transport, automatic reconnect with backoff, and serves MoQT (WebTransport) so subscribers connect directly. The previous push-only `qumo rtsp` (ANNOUNCE/RECORD server) is now `qumo rtsp-push`. Also: `UnmarshalRTP` now correctly skips CSRC/header-extension and strips padding (needed for cameras that set those bits), and the SDP-media → track construction is factored into a shared helper used by both push and pull.
- **Demo live stats overlay (`playground`):** Both boards now show a real-time stats readout over the preview while a stream is active (#139) — resolution, fps, and media bitrate from a 1-second rolling meter (`stats.ts` `createStatsTicker`), plus encoder queue depth on publish and decoder queue depth + session RTT on subscribe (RTT from `session.getStats()`). The overlay is positioned out of the core video area, updates once per second, and clears on stop.
- **Demo actionable error messages (`playground`):** Publish/subscribe failures now surface as short, actionable messages instead of bare `Error: <opaque string>` (#138). A new `errors.ts` classifier maps the recognizable cases — denied camera/microphone access, no device, device busy, unsupported quality/codec, and MoQ subscribe failures (`TrackNotFound` → "No stream at this path yet", timeout, unauthorized) — to guidance that tells the user what to do; unrecognized errors fall back to a cleaned first line with no stack traces. `media.ts` no longer rewraps `getUserMedia`/`getDisplayMedia` errors (which dropped the `DOMException.name` the classifier keys on); the previously-uncaught encoder-config call in `PublishBoard` is now caught (releasing the acquired camera on failure); and subscribe errors that were `console.warn`-only now reach the UI. Connection-failure reasons are stripped of control characters and length-clamped.
- **Demo encode-quality + viewer controls (`playground`):** The publish board now exposes resolution (480p/720p/1080p), framerate (24/30/60), and bitrate (0.5–6 Mbps) picks that drive the camera capture and the encoder — stop and restart to apply a change (#135). The subscribe board gained mute, volume, and fullscreen viewer controls (#136) — pure client-side (WebAudio gain + Fullscreen API), no transport. Volume/mute use `AudioDecodeNode.gain` (it extends `GainNode`) and fullscreen uses the Fullscreen API on the video container; MoQ is live, so there is deliberately no pause/seek/scrub.
- **RTMP ingest codec init-data builders (`internal/ingest`):** `BuildAVCDecoderConfigurationRecord` and `BuildAudioSpecificConfig` serialize the parsed AVC/AAC configs into the codec initialization blobs a browser WebCodecs decoder expects as its `description` — the same shape the browser-publish path emits.
- **ffmpeg publisher driver supports RTSP (`internal/ffpub`):** `ffpub` now publishes to `rtsp://` URLs (forced TCP interleaving) as well as `rtmp://`, driving the RTSP interop test and any future RTSP push scenarios.
- **`qumo playground` subcommand (`internal/playground`, `main.go`, `embed.go`):** A one-command local demo. A single self-contained binary generates/caches a 14-day dev WebTransport cert, starts the relay in-process, serves the embedded web UI over HTTP on `127.0.0.1:8080`, and exposes runtime configuration to the browser via a `/config` endpoint. The cert hash moves from a build-time `VITE_CERT_HASH` constant (which forced rebuilding the UI on every cert change) to a runtime `/config` fetch, with the existing `import.meta.env` values retained as a fallback so the `mage web` Vite dev workflow is unchanged. The UI is embedded via `//go:embed all:playground/dist` at the repo root (embed paths can't traverse `..`); `mage build` runs `mage webbuild` first so `bin/qumo` ships with the UI baked in, and a committed placeholder `index.html` keeps `go build` / `go install` working on a fresh clone before the UI is built. There is deliberately no `--host` flag: the browser-facing relay URL is derived per-request from the host the UI was opened at (`X-Forwarded-Host` honored behind a reverse proxy), so public hosting behind a TLS-terminating proxy needs only `--relay-addr 0.0.0.0:4433` — the dev cert is pinned by SHA-256 hash, so it works on a public host without regeneration. Only `--ui-addr` / `--relay-addr` (bind addresses) are flags. The app name is a fixed `qumo` (the former `VITE_APP_NAME` env var was dropped).
- **Demo publish source switcher (`playground`):** The publish board's media-source picker is now a segmented toggle (Camera with a camera glyph / Screen with a monitor glyph — icons from the public `lucide-solid` icon set, stroked with `currentColor`) instead of a dropdown, matching the scenario-picker style and making the two sources discoverable at a glance. The segmented-control styling was promoted to a shared `.segmented`/`.segmented-btn` class reused by the scenario picker, and the "Streaming from" status line now shows the friendly label ("Camera"/"Screen") rather than the raw signal value. The switch is disabled while streaming (stop to switch sources).

### Changed

- **Playground pull API is now testable (`internal/playground`):** the RTSP-pull handlers (`/api/pull`, `/api/pull/stop`, `/api/pull/status`) are now backed by a `pullHandle` interface + an injectable `pullStarter` (production default unchanged — `ingest.PullAndServe`), so they can be exercised without binding a real QUIC listener or presenting a cert. Internal refactor; no behavior or public-API change.
- **Relay `--role` flag replaces the `ROLE` env var (`internal/relay`, `main.go`):** the node topology role is an execution mode, not deployment config, so it is now a discoverable flag — `qumo relay --role hub` (or `edge`; omit for a flat / single-node relay). The `ROLE` environment variable is **removed** (no fallback) to avoid two sources of truth and the misconfiguration that brings — this is a breaking change for deployments that set `ROLE=…`; switch to `relay --role …`. Secrets and deployment configuration (tokens, certificates, `RELAY_ADDR`, `PEERS`, …) remain env vars. `qumo relay --help` prints the flag summary; positional args are rejected so `qumo relay hub` does not silently mean nothing. Docker / README examples updated.
- **Relay capacity knobs now take effect (`internal/relay`):** `GROUP_CACHE_SIZE` and `FRAME_CAPACITY` were read into `Config` but never consumed — the per-track group ring and frame pool were hardcoded to `DefaultGroupCacheSize` (8) and `DefaultFramePool`. They are now wired through the track manager: a positive `GroupCacheSize` sizes each track's ring, a positive `FrameCapacity` mints a per-node `FramePool`; ≤0 / unset fall back to the defaults. The `GROUP_CACHE_SIZE` default in `relay-config.example.env` is corrected from 100 (the value the relay was ignoring) to 8 to match actual behavior. The tautological `config_test.go` (which asserted struct-field round-trip, not behavior) is replaced with tests of the default-resolution logic.
- **Removed unused relay `REGION` config (`internal/relay`):** `Config.Region` / the `REGION` env var were logged at startup but never consulted — routing is role-based (`ROLE`), and the resolver-side `Region` on discovered peers (a separate field) is also unread. The field, env read, startup log line, and `relay-config.example.env` / `docker/README.md` references are removed. (Region-based routing can be re-added when actually implemented.)
- **Dependency updates — minor/patch (`go.mod`, `playground`):** Bundled demo/frontend and Go module dependency refresh, in-range minor/patch only. Frontend (`@qumo/moq` 0.16.1 → 0.16.2, `solid-js` 1.9.12 → 1.9.14, `@types/node` 25.6 → 25.9, `vite` 7.3.2 → 7.3.6) and Go indirects (`prometheus/common`, `prometheus/procfs`, `golang.org/x/net`, `golang.org/x/text`, `google.golang.org/protobuf`, et al.). `go mod tidy` dropped the unused `go.yaml.in/yaml/v2`. Build, type-check (`deno check`), and the full test suite pass. Major-version bumps (`typescript` 6, `vite` 8, `@types/node` 26) are tracked separately.

### Fixed

- **Playground pull API validates URL + broadcast path (`internal/playground`):** `/api/pull` now rejects URLs that are not `rtsp://`/`rtspd://` (or lack a host) and broadcast paths that aren't a `/`-prefixed, URL-safe-charset, length-bounded string — defense-in-depth against SSRF and log-injection (moqt only requires a leading `/`, so a path with spaces/control chars/shell metacharacters would otherwise be logged verbatim and used as a routing key). Private/LAN hosts remain intentionally allowed — pulling from an IP camera on the local network is the feature's primary use case. The playground is a local dev tool; its `/api/pull` must not be reachable in a publicly-hosted deployment.
- **Embedded version no longer carries a `-dirty` suffix (`magefiles`, `playground`):** `mage build` computed the git version via `git describe --dirty` *after* `WebBuild`, which overwrites the committed `playground/dist/index.html` placeholder on every build — so the version baked into the binary (and reported by `qumo version`) always carried `-dirty`, which would have shipped in the release string (e.g. `v0.4.0-dirty`). The version is now captured *before* `WebBuild`, so a build from a clean tag checkout yields a clean `v0.4.0`. `playground/deno.lock` was also under-resolved (the `av-nodes@0.10.4` entry was missing its `dependencies` edge), so every Deno run re-added it and dirtied the tree — the committed lock now matches what Deno produces.
- **Quieter `mage web` dev console (`playground`):** `config.ts` no longer fires a `GET /config` 404 on every Vite-dev load (the dev server doesn't serve `/config`; only the built `qumo playground` binary does) — it skips the fetch when `import.meta.env.DEV` and reads `import.meta.env` directly, with the built UI unchanged. Also removed the left-in first-10-frames hex-dump diagnostic (`[Subscribe] video #N … hex=[…]`) that flooded the console on each subscribe.
- **Subscribe-to-empty-path no longer misreports a relay connection failure (`playground`):** when the relay reset a subscribe stream before responding and the MoQ error code wasn't carried, the demo surfaced the raw `WebTransportError: Received RESET_STREAM` and — because that string contains "WebTransport" — mis-classified it as *"Could not connect to the relay."* The relay was reachable; the path just had no publisher. `errors.ts` now recognizes a subscribe-context stream reset and maps it to the same actionable *"No stream at this path yet. Make sure the publisher — or the RTMP/RTSP pusher — is running, then click Start again."* text used for `TrackNotFound`.
- **Cross-origin WebTransport rejections are now logged (`internal/cors`):** `NewChecker` silently rejected browser requests whose `Origin` wasn't on the allow-list, so a misconfigured `CORS_ALLOWED_ORIGINS` surfaced only as a browser-side `ERR_CONNECTION_RESET` with no server-side trace — making the common "browser can't connect" cause hard to diagnose. The checker now emits a `slog.Info` on rejection with the origin, the request host, and a remediation hint. (Accept/reject behavior is unchanged.)
- **RTSP-ingested streams no longer play back with a broken picture and audio pops (`internal/ingest`):** Two independent RTSP ingest defects made RTSP playback unwatchable (corrupted/tearing video and clicking audio) where the same `Session`/player played RTMP fine. **Video:** ffmpeg's RTSP muxer emits several IDR NALUs at the same RTP timestamp within one access unit; ingest pushed one MoQT frame per NALU, so a keyframe produced several same-PTS frames in one group, only the first of which the player marks `key` — the rest were fed to WebCodecs as `delta` chunks at an identical timestamp and decoded as competing pictures. H.264 depacketization now aggregates every NALU sharing an RTP timestamp into one AVCC sample (one access unit → one frame), with the boundary detected by RTP-timestamp change plus the marker bit, and now also splits STAP-A aggregation packets (previously dropped). **Audio:** an mpeg4-generic RTP packet packs 3–4 AAC access units; ingest pushed each as its own MoQT group, so one packet burst N concurrent QUIC streams (MoQT maps a group to a stream) that gomoqt delivers in stream-arrival order — the subscriber received AAC frames out of PTS order and the decoder popped. `Session.PushAudioFrames` now coalesces one packet's access units into a single multi-frame group, keeping them on one stream in order. Regression guard: `TestRTSPPlayback_FrameIntegrity` (integration) asserts video frames have unique PTS and audio frames arrive monotonically; unit tests cover STAP-A splitting, access-unit aggregation, and the coalesced-audio group path.
- **Demo no longer shows a false "WebTransport will reject" cert warning under mkcert (`playground`):** `ConnectionStatus` warned whenever `VITE_CERT_HASH` was unset — exactly the state the mkcert path creates by clearing it, since the cert is trusted via the local root CA and needs no pin. `cert.ts`'s `buildTransportOptions` now treats a missing hash as a non-problem (only a malformed hash is flagged), so mkcert users no longer see the wrong remediation; a genuinely-forgotten self-signed cert still surfaces via the connection-error path.
- **`qumo playground` rejects an un-pinnable shared cert (`internal/playground`):** `loadCertIfFresh` now refuses a cached cert whose validity exceeds the 14-day WebTransport `serverCertificateHashes` limit (e.g. a mkcert cert pulled in via `QUMO_PLAYGROUND_CERT_DIR=certs`) instead of serving a hash Chrome would reject — and without clobbering the shared file. The error tells the user to unset `QUMO_PLAYGROUND_CERT_DIR` or use a ≤14d cert.
- **RTSP ingest no longer creates spurious PTS regressions from redundant same-timestamp IDRs (`internal/ingest`):** ffmpeg's RTSP muxer intermittently emits several IDR NALUs back-to-back at the same presentation timestamp within one access unit. The ingest opened a fresh MoQT group on every keyframe NALU, so this produced rapid micro-group churn that the relay ring / a bounded collector window delivered out of order — observed downstream as a deterministic ~1.97 s backward PTS jump at a GOP boundary that intermittently flaked `TestRTSPInterop_Matrix/gop60_720p30` on CI (#229). `videoTrack.push` now opens a new group only when a keyframe's timestamp differs from the group being filled, collapsing same-AU IDRs into one group. For all well-behaved streams (one IDR per access unit, distinct timestamps) behavior is unchanged; the RTMP path (one access unit per push) is unaffected. The matrix case's widened-stopgap `MaxCTSWindowUS` is reverted to the shared 1 s window, and a wide-net `TestRTSP_PTSMonotonic` guard is added.
- **`qumo playground` now connects from the browser out of the box (`internal/playground`, `internal/cors`):** The one-command demo serves the UI and the relay on different ports, so the browser's WebTransport was cross-origin and rejected by the same-origin default (the same root cause #224 fixed for `qumo relay`). `configureRelayEnv` now defaults `CORS_ALLOWED_ORIGINS=same-host` when unset. `internal/cors` gained a `same-host` allow-list entry (port-insensitive host match) that fits playground's design exactly — the relay URL is derived per-request from the browser's own Host, so `Origin.Host` and the relay `Host` always share a host; a different host is still rejected. An explicit `CORS_ALLOWED_ORIGINS` value is respected. Closes #225.
- **Relay now honors `CORS_ALLOWED_ORIGINS` for browser WebTransport (`internal/relay`, `internal/cors`):** The main relay's `WebTransportHandler` left `CheckOrigin` unset, so it fell back to webtransport-go's same-origin-only default — meaning no browser could connect to the relay when the UI was served from a different origin (the entire local `mage web` Vite dev workflow, and any multi-origin deploy). The Go interop client passed only because it sends no `Origin` header. The standalone RTMP/RTSP ingest servers already read `CORS_ALLOWED_ORIGINS` (#141); that logic is now extracted into a shared `internal/cors` package (`LoadAllowed` + `NewChecker`, secure same-origin default, `*` opt-out) used by **both** the relay and the ingest servers, and the relay wires it into its `WebTransportHandler`. Default behaviour is unchanged (same-origin only); set `CORS_ALLOWED_ORIGINS=http://localhost:5178` (etc.) to allow the dev UI's origin.
- **Playground production UI build repaired (`playground`):** `npm run build` / `mage webbuild` was broken by two pre-existing issues. (1) `@deno/vite-plugin` 1.0.6 crashed under vite 7 / deno 2.8 (`resolveViteSpecifier: Cannot read properties of undefined (reading 'startsWith')`) — upgraded to `^2.0.2`, which supports vite 5–8 and current deno, so `vite build` now succeeds. (2) the build's `tsc -b` type-check could not resolve jsr imports (`@qumo/moq`, `@okdaichi/*`) because standalone `tsc` ignores `deno.json`'s import map; the build script now type-checks via `deno check src` (which resolves jsr and passes cleanly) before `vite build`.
- **RTSP ingest now works with ffmpeg (`internal/ingest`):** Two protocol bugs surfaced by the RTSP interop test, either of which aborted every ffmpeg RTSP publish: (1) the SETUP handler parsed the Transport header with a strict `Sscanf("RTP/AVP/TCP;interleaved=%d-%d")` that rejected ffmpeg's actual `RTP/AVP/TCP;unicast;interleaved=0-1` (extra `;unicast;`) with 400 Bad Request — now parsed robustly via `parseInterleavedChannels`; (2) AAC track detection was case-sensitive (`mpeg4-generic`) while ffmpeg emits `MPEG4-GENERIC` — codec detection is now case-insensitive, and an empty SDP `a=control` no longer matches every SETUP.
- **Relay no longer panics without `REMOTE_RESOLVER_URL` (`internal/relay`):** `relay.Run` called `remoteResolver.Interval()` unconditionally, but `NewRemoteResolver` returns nil when `REMOTE_RESOLVER_URL` is unset (the common single-node/demo case), panicking at startup. The resolver is already treated as optional by its consumer (`server.go`), so the call is now guarded. This also unblocks `qumo playground`, which starts the relay without a remote resolver.
- **Ingested AAC audio now plays in the demo subscriber (`playground`):** RTSP/RTMP ingest publishes AAC with its AudioSpecificConfig (ASC) as Base64 catalog `initData`, but the subscribe path decoded `initData` into the WebCodecs `description` only for video — the `AudioDecoder` was configured with just `codec`/`sampleRate`/`numberOfChannels` and no ASC, so raw AAC frames (no ADTS, as ingest emits) failed to decode and ingest audio was silent while video played fine. `SubscribeBoard` now Base64-decodes `audioTrack.initData` into the `AudioDecoder` `description`, mirroring the video path.

### Changed

- **Major-version frontend dependency bumps (`playground`):** `typescript` 5.9 → 6.0, `vite` 7.3 → 8.1, `@types/node` 25 → 26. Type-check (`deno check`) and the vite production build pass with the new majors. Split out from the minor/patch refresh (#267) so the major jumps are evaluated in isolation.
- **`mage relay` + `mage web` now connect out of the box; relay defaults to a dual-stack bind (`internal/relay`, `magefiles`, `playground`):** the relay's default `RELAY_ADDR` changed from `0.0.0.0:4433` to `:4433`, which binds both IPv4 and IPv6 — so `https://localhost:4433` works on hosts where `localhost` resolves to `::1` (e.g. Windows), which previously reset the browser's WebTransport handshake. The `mage relay` dev wrapper now also applies dev-friendly defaults when unset — `ADVERTISE_ADDR=localhost:4433` (required for the wildcard bind) and `CORS_ALLOWED_ORIGINS` allowing the Vite dev UI origins — so `mage relay` alongside `mage web` no longer needs manual env setup. User-set env always wins; the standalone `qumo relay` binary keeps its secure same-origin CORS default. Refs #234.
- **`mage cert` prefers mkcert for local dev (`magefiles`, `playground`, `docker`):** `mage cert` now signs the localhost WebTransport cert with [mkcert](https://github.com/FiloSottile/mkcert) when it's on PATH, producing a long-lived cert that chains to a trusted local root CA — so the browser trusts it directly and no `VITE_CERT_HASH` pinning, no 14-day re-run, and no Vite restart are needed. `mkcert -install` runs first; if it fails (e.g. the Linux system store needs root) `mage cert` falls back to the self-signed path rather than signing an untrusted cert and wiping the working `VITE_CERT_HASH` pin. A stale `VITE_CERT_HASH` (and its comment) from a prior self-signed run is cleared from `playground/.env` so it can't pin the wrong cert. A new `CERT_HOSTS` env var (comma-separated, mkcert path only) appends extra SANs beyond the default `localhost`/`127.0.0.1`/`::1`, so the cert also validates when the demo is reached from another device on the LAN (e.g. `CERT_HOSTS=192.168.1.10,desktop.local mage cert`). When mkcert is absent (CI, headless, air-gapped), `mage cert` falls back to the previous 14-day self-signed ECDSA cert and writes `VITE_CERT_HASH` as before, so those workflows are unchanged. The self-signed key is now written at 0600 (was world-readable). Closes #196.
- **Release artifacts now embed the real playground UI (`playground`, `docker`, `.github`):** goreleaser and the Docker image build previously ran `go build` from a fresh checkout, so `//go:embed all:playground/dist` embedded only the committed placeholder and `qumo playground` shipped the "UI not built" page. goreleaser now builds the UI in a `before` hook (`cd playground && deno install && deno task build`), with a `denoland/setup-deno` step added to `release.yml`; the Dockerfile gained a `node:22-alpine`-based frontend stage (Deno installed for jsr/`deno check`, Node for Vite) whose `dist` is overlaid into the Go build stage. `.dockerignore` now admits the playground source (excluding `node_modules`, host `dist`, `.env`) so the frontend stage can build. Tagged releases and the published image now serve the real demo UI.
- **RTMP/RTSP ingest forward AVCC unchanged (`internal/ingest`):** RTMP ingest no longer converts AVCC to Annex-B — video frames are passed through as AVCC (length-prefixed NALUs) with the codec string switched from `avc3` to `avc1`. RTSP ingest was moved onto the same format (NALUs are now AVCC-length-prefixed via `wrapAVCC` instead of Annex-B start codes, and its config sets `NALULenSize: 4`). Video/audio catalog tracks now carry Base64-encoded `initData` built from the sequence header. This conforms both ingest paths to the same MoQT wire format the browser-publish path emits, making ingested streams browser-decodable. `AVCCToAnnexB` and its tests were removed; PTS = DTS + CTS preserves B-frame timing via `parseFLVVideoCTS`.

- **Bumped gomoqt to v0.16.0 (`go.mod`):** Upgraded the MoQT library from v0.15.0. Notable upstream fixes carried in by this release include a critical OOM denial-of-service fix for unconstrained varint allocation, `Server.Close()` no longer hanging with active connections, and `OpenGroup`/`OpenGroupAt` backpressuring on the QUIC uni-stream limit instead of aborting. `OpenGroup`/`OpenGroupAt` now require a `context.Context`; call sites in `internal/ingest`, `internal/relay`, and `internal/smoketest` were updated accordingly.
- **Bumped gomoqt to v0.16.1 (`go.mod`):** Carries an updated `webtransport-go` fork (v0.11.0-okdaichi.1) that fixes a `Session.CloseWithError` deadlock — the close blocked on an internal `sync.WaitGroup` waiting for stuck stream goroutines, hanging the interop test matrix ~50% of runs on Windows. The RTMP interop matrix is now 10/10 stable (was ~50% pass). Closes #205.
- **groupCache concurrency model — RCU / copy-on-write (`internal/relay`):** Replaced the slice + atomic-length lockless-read scheme (which carried a benign data race on the slice header, kept non-fatal only by the never-shrink invariant) with an `atomic.Pointer`-published immutable-snapshot design. `append` is now copy-on-write via a compare-and-swap loop (safe under concurrent writers); `next` loads an immutable snapshot — reads stay lock-free and zero-allocation (~0.29 ns/op, unchanged) and are now data-race-free under the Go memory model. Trade-off: appends become O(n) copy-on-write (higher write cost) in exchange for formal race-freedom and the ability to safely reset a live cache. Removed the now-unneeded `sync.RWMutex` and `frameLen` fields.
- **Web demo directory renamed `solid-deno` → `playground`:** The relay's browser demo / test client was named after its original tech stack (SolidJS + Deno); renamed to `playground` to describe its role. All references updated — the magefile `web` / `webBuild` / `cert` targets and path literals, the docker compose demo config, `README.md`, `.dockerignore`, and the in-app `package.json` name — with full file history preserved via a pure `git mv`. Historical mentions of `solid-deno` elsewhere in this changelog are intentionally left as-is.
- **Bundled demo deps (`playground`):** `@okdaichi/av-nodes` 0.10.3 → 0.10.4 (the encode loop stops instead of spinning when `encode()` throws on an unconfigured codec) and `@okdaichi/golikejs` 0.9.0 → 0.10.0. `@okdaichi/media-log` was already at the latest in range.

### Performance

- **Relay Handler Egress Allocation Optimization:** Extracted `string(tw.TrackName)` conversion outside the wait loop in the track distributor egress handler, preventing unnecessary memory allocations in the tight loop.

### Removed

- **Bootstrap server removed (`internal/bootstrap`):** The bootstrap discovery server
  and client (`qumo bootstrap` command) have been removed from this repository.
  Bootstrap functionality with traffic engineering is being migrated to the
  qumo-enterprise repository as a control plane service.
- **Removed stale `examples/web-demo/`:** An orphan README pointing at a defunct JSR-based demo; the live web demo lives in `playground/` (formerly `solid-deno/`), where all `mage web` targets already pointed.

### Security

- **RTMP listener hardened against handshake stalls and bad clients (`internal/rtmp`):** `Listener.Accept` now runs the RTMP handshake under a read deadline (default 10s), so a client that connects and then stalls can no longer hold the accept loop and block every other RTMP connection. Handshake failures (a stalled, half-open, or otherwise-misbehaving client) are closed and skipped instead of returned as an Accept error — previously a single failed handshake took down the whole ingest server, since server accept loops treat any Accept error as fatal. Skipped handshakes are logged at debug level for observability.
- **Bumped `golang.org/x/crypto` to v0.53.0 (`go.mod`):** Clears a set of `golang.org/x/crypto/ssh` HIGH-severity CVEs (CVE-2026-39829, -39830, -39832, -39835, -42508, -46595, -46597) present in the previously-resolved v0.51.0, which the SHA-pinned Trivy image scan now reports end-to-end — that scan only became functional once `docker.yml` builds are loaded into the local Docker daemon. `govulncheck` confirms the vulnerable `ssh` package is not reached by qumo's code, but bumping the module removes the finding at the source. Also pulls `golang.org/x/sys` → v0.46.0 and `golang.org/x/text` → v0.38.0.
- **CORS origin check hardened (`internal/ingest`):** `WebTransportHandler.CheckOrigin` no longer accepts every origin for the RTMP and RTSP ingest servers. Origins are validated against a comma-separated `CORS_ALLOWED_ORIGINS` environment variable (supporting a `*` wildcard), with a same-origin fallback, closing a WebTransport cross-site request forgery risk.
- **TLS configuration hardened (`internal/relay`):** Removed `InsecureSkipVerify` from the relay dialer, enforcing proper TLS verification on outgoing connections to prevent Man-in-the-Middle attacks.
- **Removed dynamic TLS generation:** Removed the capability to dynamically generate self-signed TLS certificates in production binaries when `INSECURE=true`. Test suites have been updated to utilize dynamically generated temporary certificates.
- **Dependency and image vulnerability scanning (`.github/workflows`):** Added a
  `govulncheck` job to `ci.yml` that runs on every PR/push and fails when a dependency
  carries a known Go vulnerability, plus a new `nightly.yml` that re-scans `main` on a
  daily schedule to catch CVEs disclosed after a dependency was already merged. Added a
  SHA-pinned Trivy scan to `docker.yml` (`severity: CRITICAL,HIGH`, `ignore-unfixed`,
  `exit-code: 1`) over the locally-built image. Trivy is pinned to commit `57a97c7`
  (`trivy-action@0.35.0`) rather than a mutable tag: `trivy-action` tags were
  force-pushed in the March 2026 supply-chain attack (CVE-2026-26189 / GHSA-69fq-xp46-6x23).

### Performance

- **Replaced `time.After` with `time.Timer` inside loops:** Removed `time.After` inside `for` loops in `internal/relay/handler.go` and `internal/ingest/handler.go`, eliminating allocations and significantly lowering GC footprint and latency.
- **Optimized `time.After` usage (`internal/relay`):** Replaced `time.After` in busy loops within `handler.go` with a reusable `time.NewTimer` to prevent memory allocations per iteration, reducing garbage collector overhead.
- **Optimized FLV AVC parsing (`internal/ingest`):** Improved `ParseAVCConfig` by implementing a safe, two-pass parsing algorithm that dramatically reduces garbage collector stress by removing slice allocations within SPS/PPS loops.

### Added

- **Demo UI foundation (`solid-deno`):** Turned the unmodified Vite+Solid template into a usable AV streaming demo. Defined all previously-undefined board CSS classes and made the layout responsive — a 2-column board grid that stacks on narrow viewports with video previews scaling to their track — plus light/dark theme tokens (#133). Renamed the misspelled `Dashborad` component to `Dashboard`, set a real `<title>`, and replaced the template README with demo run instructions (`mage cert` / `mage relay` / `mage web`) (#132). Added a live WebTransport connection-status indicator (connecting → connected → closed/failed) that also surfaces a mid-session relay disconnect — distinguishing a graceful close from a transport error via `Session.closed` — with a user-facing reason and cert-hash remediation (missing or malformed) instead of a silent `console.warn` (#134). `VITE_CERT_HASH` is now validated (exactly 64 hex chars) so a malformed value can't silently produce a wrong pin. Bumped `@qumo/moq` to `^0.16.1`.
- **Demo scenario selector + working echo + RTMP/RTSP subscribe (`solid-deno`):** Added a segmented scenario picker (Echo / RTMP ingest / RTSP ingest) and a single shared, editable, shareable broadcast path (`?scenario=` + `?path=` deep links, copy-path / copy-link buttons), replacing the hidden `/${username}` path that broke the round-trip out of the box (#137). The MoQ-MoQ echo now works end-to-end (publish in one peer, subscribe in another at the same path). RTMP and RTSP ingest scenarios are subscribe-only (publish board hidden) against their own WebTransport origins (`:4443` / `:4543`), each showing a copy-pasteable ffmpeg push command feeding `/live/demo` (#141). Switching scenarios reconnects via a keyed remount that closes the prior session. Removed the dead `useBroadcastPath`/`UserProvider` path plumbing; lifted cert-hash parsing into `src/cert.ts`.
- **Local multi-scenario demo environment (`docker/docker-compose.demo.yml`, `magefiles`):** Brings the relay (MoQ-MoQ echo) and the RTMP/RTSP ingest origins up together so every demo pipeline is testable locally without reconfiguring. The RTMP/RTSP servers are standalone WebTransport origins (they do not dial the relay), so all three share one `mage cert` cert and a single pinned `VITE_CERT_HASH`. Adds a `mage demo:` namespace (`up`/`push`/`down`/`logs`/`ps`) — `demo:up` generates the cert only if missing — plus opt-in ffmpeg test-pattern pushers (compose profile `push`) feeding `/live/demo`. Also corrects stale `INSECURE` Docker docs (auto-self-sign was removed; mount certs / use `mage cert`).
- **RTSP Ingest Server (`internal/ingest`, `internal/rtsp`):** Implemented a complete RTSP 1.0 ingest server to bridge IP cameras and traditional encoders to MoQT.
  - *Protocol Stack*: Custom RTSP implementation including request/response parsing, interleaved binary framing over TCP, and SDP/RTP support.
  - *Media De-packetization*: H.264 (FU-A fragmentation) and AAC (mpeg4-generic, RFC 3640) RTP de-packetizers reconstruct NAL units and audio access units for MoQT delivery.
  - *CLI Command*: New `qumo rtsp` command to start a standalone RTSP-to-MoQT bridge.
  - *Mage Targets*: Added `rtsp:serve` for running the server, `rtsp:stream` for pushing test patterns with ffmpeg, and `rtsp:demo` for quick environment setup.
- **Nomad LocalResolver simulation (`docker/docker-compose.nomad.yml`, `docker/nomad/`):**
  A real single-region Nomad dev cluster (2 hubs + 2 edges) that exercises the
  `LocalResolver` (Nomad native service discovery) path — edges discover local
  hubs via Nomad and connect. Verifiable via the `qumo_relay_peers_connected`
  metric. Manual simulation only; no automated integration tests. Cross-region
  hub discovery (the `RemoteResolver`/`/peers` path) is explicitly out of scope.
- **Peer resolver interface (`internal/relay/resolver.go`):** New `PeerResolver`
  interface with `ResolvePeers(ctx, query)` method, `ResolvedPeer` and `PeerQuery`
  types. Enables pluggable peer discovery backends.
- **CredentialClient (`internal/relay/credential_client.go`):** Optional backend
  integration for publisher credential authentication and usage metering.
  When `QUMO_CREDENTIAL_URL` is set the relay authenticates each WebTransport
  ANNOUNCE by subscribing to a well-known `"auth"` MoQ track on the announced
  broadcast path (5 s timeout), reading the JWT from the first frame, and
  calling `POST /v1/credentials/introspect`. Announcements with missing or
  rejected credentials are silently dropped. Valid credentials are cached until
  the server-supplied `revalidate_after` time; concurrent requests for the same
  JWT are coalesced via `singleflight`; expired cache entries are swept on each
  write. A `broadcastSession` UUID is minted per accepted announcement and
  cumulative `gateway.ingress_bytes` / `gateway.egress_bytes` totals are
  reported to `POST /v1/usage/events` every 30 s and on session close.
  New env vars: `QUMO_CREDENTIAL_URL` (base URL) and `QUMO_RELAY_TOKEN` (shared
  bearer token). When both vars are absent the relay behaves as before (open mode).
- **LocalResolver (`internal/relay/local_resolver.go`):** Within-cluster peer
  discovery via Nomad native service discovery API. Configured via `LOCAL_RESOLVER_ADDR`,
  `LOCAL_RESOLVER_SERVICE_NAME`, and `LOCAL_RESOLVER_INTERVAL` environment variables.
- **RemoteResolver (`internal/relay/remote_resolver.go`):** Cross-cluster peer
  discovery via an external traffic resolver API (e.g. qumo-enterprise).
  Configured via `REMOTE_RESOLVER_URL`, `REMOTE_AUTH_TOKEN`,
  `REMOTE_RESOLVE_INTERVAL`, and `REMOTE_TLS_ENABLED`.
- **In-process discovery integration test (`internal/relay`, build tag `integration`):**
  Stands up a real edge + hub relay and a fake Nomad service catalog, asserting the
  edge discovers the hub via `LocalResolver` and completes a real QUIC/MOQT handshake.
  Kept out of the default `go test ./...` unit run; gated by a dedicated `Integration`
  CI job (`go test -tags=integration`).

### Changed

- **Renamed `docker-compose.topology.yml` → `docker-compose.static.yml`:** clarifies
  that it wires peers via static `PEERS` (no discovery), distinct from the new
  `docker-compose.nomad.yml` which exercises Nomad service discovery.
- **Publisher vs. peer-relay session split (`internal/relay/server.go`):** Native
  QUIC sessions (relay peers, ALPN `moqt`) are now handled by a dedicated
  `relayPeer` path that bypasses credential auth. WebTransport sessions
  (publishers and browsers, ALPN `h3`) go through `Relay` and require credential
  auth when `QUMO_CREDENTIAL_URL` is set. This distinction is wired in `Server.init`
  by setting separate handler funcs on `MOQServer.Handler` vs `WebTransportHandler`.
- **`group_cache.fill` onFrame callback (`internal/relay/group_cache.go`):**
  The `onFrame` parameter changed from `func()` to `func(n int)` where `n` is
  the frame's byte length (0 on the group-completion call). This lets callers
  accumulate ingress byte totals in the same pass without re-reading cached frames.
- **Relay topology (`internal/relay/server.go`):** Updated peer discovery topology.
  Edges connect to all local hubs (load-balanced). Hubs connect only to remote
  hubs via the remote resolver (no local hub↔hub connections).
- **`internal/relay/cmd.go`:** Replaced `BOOTSTRAP_URLS`/`BOOTSTRAP_INTERVAL` env
  vars with `LOCAL_RESOLVER_*` and `REMOTE_*` resolver configuration.
- **`main.go`:** Removed `qumo bootstrap` command.
- **RemoteResolver `/peers` role handling (`internal/relay/remote_resolver.go`):**
  Stopped sending `?role=hub` and dropped the client-side re-filter on the response
  `role` field, ahead of the control-plane registry going hub-only
  (foalk-inc/qumo-deploy#535). A peer's role now falls back to the queried role only
  when the response omits it. Prevents silently dropping every hub once the registry
  stops returning `role`. (#93)
- **CI Go version source and concurrency (`.github/workflows`):** Switched `setup-go`
  in `ci.yml`, `release.yml`, and `nightly.yml` from a hardcoded `go-version: '1.26'` to
  `go-version-file: go.mod`, making `go.mod` the single source of truth (matching
  `bench.yml`). Added `concurrency` groups with `cancel-in-progress: true` to `ci.yml`
  and `docker.yml` to cancel superseded runs; intentionally omitted from `release.yml`
  so tag-triggered releases are never canceled mid-run.

### Added

- **Concurrent group fill limiting (`internal/relay`):** A buffered-channel semaphore
  (`fillSem`) now bounds the number of in-flight fill goroutines per `trackDistributor`
  to `MaxGroupFillsInFlight` (default `max(32, 2×GOMAXPROCS)`). When all slots are
  occupied, `ingest` blocks on the semaphore rather than spawning unboundedly, providing
  natural backpressure against bursty or slow-consumer ingest. A new Prometheus gauge
  `qumo_relay_group_fills_inflight` exposes the current in-flight count for observability.
  `MaxGroupFillsInFlight` is a package-level variable and can be overridden before
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
- **`docker-compose.static.yml` port protocols:** UDP and TCP protocols are now
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

- **`ingressCounter` never incremented (`internal/relay/handler.go`):** The
  `trackDistributor.ingressCounter` Prometheus counter was allocated but never
  updated in the data path. Ingress bytes are now counted inside the `fill`
  callback in `processGroup` using the new `onFrame(n int)` signature.
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
- **Smoke test optimized:** Replaced `fmt.Sprintf` with `strconv.AppendInt` inside
  `generateTestData` nested loop to avoid heavy reflection and large allocations.
  Memory usage and allocations significantly reduced, yielding ~60% faster test payloads generation.

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

[Unreleased]: https://github.com/qumo-dev/qumo/compare/v0.3.1...HEAD
[v0.4.0]: https://github.com/qumo-dev/qumo/compare/v0.3.1...HEAD
[v0.3.1]: https://github.com/qumo-dev/qumo/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/qumo-dev/qumo/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/qumo-dev/qumo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/qumo-dev/qumo/releases/tag/v0.1.0

