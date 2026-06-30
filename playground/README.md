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

The demo needs a running relay plus the Vite dev server. From the **repo root**:

```bash
# 1. Generate a WebTransport cert (writes VITE_CERT_HASH to playground/.env)
mage cert

# 2. Terminal A — start the relay
mage relay        # or: ./bin/qumo-relay

# 3. Terminal B — start the demo
mage web
```

Then open <http://localhost:5173>.

The cert hash is required: Chrome's `serverCertificateHashes` rejects the relay's
self-signed cert without it, so without `VITE_CERT_HASH` the connection fails.
`mage cert` handles generating the cert and writing the hash; see `.env.example`.

### Configuration

Environment variables live in `playground/.env` (see `.env.example`):

| Variable          | Description                                              |
| ----------------- | -------------------------------------------------------- |
| `VITE_RELAY_URL`  | Relay WebTransport URL (must be HTTPS).                  |
| `VITE_APP_NAME`   | Title shown in the demo header.                          |
| `VITE_CERT_HASH`  | SHA-256 (hex) of the relay cert. Run `mage cert` to set. |

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

## Develop

```bash
deno task dev      # Vite dev server
npm run build      # type-check + production build to dist/
npm run preview    # preview the production build
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
