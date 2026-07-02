# qumo — AV Streaming Demo

A browser demo for experiencing audio/video live streaming over
[MoQ (Media over QUIC)](https://datatracker.ietf.org/wg/moq/about/) / WebTransport,
powered by the qumo relay.

It publishes a camera or screen-share stream to the relay over WebTransport and
subscribes back to it, exercising the MoQ-MoQ echo pipeline end to end (video +
audio). A scenario picker at the top switches between that **Echo** pipeline and
subscribe-only **RTMP / RTSP ingest** pipelines. The same UI is the foundation
for the HLS egress scenario tracked in the [Demo UI Improvements milestone][milestone].

[milestone]: https://github.com/qumo-dev/qumo/milestone/1

## Run it

There are two ways to run the demo:

- **`qumo playground`** — the self-contained distribution/demo path. A single
  binary that generates a dev cert, starts the relay in-process, serves the
  embedded UI, and exposes runtime config via `/config` (no build-time
  `VITE_CERT_HASH` needed). Build it from the repo root:

  ```bash
  mage build          # builds the UI, then the binary with the UI embedded
  ./bin/qumo playground
  ```

  Then open <http://127.0.0.1:8080>. The dev cert is cached under your per-user
  cache dir (`os.UserCacheDir`/qumo/playground) and reused until it nears
  expiry; set `QUMO_PLAYGROUND_CERT_DIR` to override its location (e.g. to share
  a cert with `mage cert`'s `./certs`).

  Flags (`qumo playground -h`):

  | Flag            | Default          | Purpose                                              |
  | --------------- | ---------------- | ---------------------------------------------------- |
  | `--ui-addr`     | `127.0.0.1:8080` | Address the UI HTTP server binds.                    |
  | `--relay-addr`  | `127.0.0.1:4433` | Address the relay WebTransport server binds.         |

  There is deliberately **no `--host` flag**. The browser-facing relay URL is
  derived at request time from whatever host the UI was opened at: open
  `http://localhost:8080` and `/config` returns `relayUrl: https://localhost:4433`;
  open `https://example.com` (behind a proxy) and it returns
  `https://example.com:4433`. So the relay target always matches the address in
  the address bar, with nothing to configure.

  ### Hosting publicly (behind your own TLS proxy)

  WebTransport requires a secure context, and `localhost` is the only HTTP host
  that counts — so to serve the demo on a public host, put a TLS-terminating
  reverse proxy (nginx/Caddy/Cloudflare) in front of the UI:

  ```bash
  # On the host: relay binds a public interface so its UDP/QUIC port is reachable;
  # UI stays loopback for the proxy to forward to.
  qumo playground --relay-addr 0.0.0.0:4433
  # Then: proxy  https://example.com  ->  127.0.0.1:8080
  #        (forward X-Forwarded-Host so /config picks up the public host)
  #        allow  UDP/4433  through your firewall for WebTransport.
  ```

  The browser loads the UI over HTTPS (from your proxy), fetches `/config`, and
  dials WebTransport to `https://example.com:4433`, pinning the dev cert by its
  SHA-256 hash. The pin is by hash, not hostname, so the localhost-minted cert
  works on a public host without regeneration.

- **`mage web`** — the frontend development path (Vite dev server + HMR). The
  demo needs a running relay plus the Vite dev server. From the **repo root**:

  ```bash
  # 1. Generate a WebTransport cert (writes VITE_CERT_HASH to playground/.env)
  mage cert

  # 2. Terminal A — start the relay
  mage relay        # or: ./bin/qumo-relay

  # 3. Terminal B — start the demo
  mage web
  ```

  Then open <http://localhost:5173>.

The cert hash is required in the dev path: Chrome's `serverCertificateHashes`
rejects the relay's self-signed cert without it, so without `VITE_CERT_HASH` the
connection fails. `mage cert` handles generating the cert and writing the hash;
see `.env.example`. (`qumo playground` sidesteps this by serving the hash at
runtime via `/config`.)

### Configuration

Environment variables live in `playground/.env` (see `.env.example`):

| Variable          | Description                                              |
| ----------------- | -------------------------------------------------------- |
| `VITE_RELAY_URL`  | Relay WebTransport URL (must be HTTPS).                  |
| `VITE_CERT_HASH`  | SHA-256 (hex) of the relay cert. Run `mage cert` to set. |

The header title is a fixed `qumo` (not configurable).

## Scenarios

Pick a scenario with the segmented tabs at the top. Each scenario is a distinct
WebTransport origin, so switching tabs reconnects. The path field is shared,
editable, and shareable — **Copy link** produces a `?scenario=&path=` URL that
opens the demo on the exact same stream.

Every default path embeds a per-session unique token (`/<name>-<id>` for echo,
`/<scheme>/<id>` for ingest) so that on a **shared public relay** no two users
collide on the same broadcast. The RTMP/RTSP tabs show a push command whose
target embeds that same unique path.

| Scenario | Origin | Default path | What it exercises | Status |
| -------- | ------ | ------------ | ----------------- | ------ |
| Echo | `https://localhost:4433` | `/<name>-<id>` (editable) | Publish → relay → subscribe (MoQ-MoQ) | Working |
| RTMP ingest | `https://localhost:4443` | `/rtmp/<id>` | Subscribe to an RTMP-pushed stream | Working (#141) |
| RTSP ingest | `https://localhost:4543` | `/rtsp/<id>` | Subscribe to an RTSP-pushed stream | Working (#141) |
| HLS | — | — | Consume the relay's HLS egress | Blocked (#142) |

For the ingest scenarios, start the demo origins and push a test stream:

```bash
mage demo:up      # relay + rtmp + rtsp origins (generates cert if missing)
mage demo:push    # opt-in ffmpeg test-pattern pushers → /rtmp/demo, /rtsp/demo
```

The RTMP/RTSP tabs also show a copy-pasteable ffmpeg push command. All origins
share one `mage cert` certificate, so a single `VITE_CERT_HASH` validates them.

## Controls

- **Publish (Echo):** resolution (480p/720p/1080p), framerate (24/30/60), and
  bitrate (0.5–6 Mbps) picks. These shape the camera capture and the encoder;
  stop and restart to apply a change mid-session.
- **Subscribe:** mute, volume, and fullscreen. These are viewer controls only —
  MoQ is live, so there is no pause/seek/scrub.
- **Stats overlay:** while a stream is active, both boards show a live readout
  over the preview — resolution, fps, media bitrate, and (publish) encoder
  queue / (subscribe) RTT and decoder queue. Updated once per second.

## Develop

```bash
deno task dev      # Vite dev server
deno task build    # type-check (deno check) + production build to dist/
deno task preview  # preview the production build

Install deps first with `deno install` (the project is Deno-managed —
`deno.lock` is the source of truth; `npm install` cannot resolve the
`@deno/vite-plugin` jsr deps).
```

## Project layout

```
src/
  App.tsx              App shell + header
  Dashboard.tsx        Scenario + path state, URL deep links, top controls
  ScenarioView.tsx     Per-scenario session, connection status, board layout
  ScenarioPicker.tsx   Echo / RTMP / RTSP tab selector (#137)
  PathControl.tsx      Shared editable path + copy/share (#137)
  PushInstructions.tsx ffmpeg push command for ingest scenarios (#141)
  ConnectionStatus.tsx WebTransport lifecycle indicator (#134)
  scenarios.ts         Scenario registry (ports, modes, push commands)
  cert.ts              VITE_CERT_HASH parsing + transport options
  publish/             Publish board: capture → encode → MoQ
  subscribe/           Subscribe board: MoQ → decode → canvas
  user/                Random-name helper (seeds the Echo default path)
```
