# qumo — AV Streaming Demo

A browser demo for experiencing audio/video live streaming over
[MoQ (Media over QUIC)](https://datatracker.ietf.org/wg/moq/about/) / WebTransport,
powered by the qumo relay.

It publishes a camera or screen-share stream to the relay over WebTransport and
subscribes back to it, exercising the MoQ-MoQ echo pipeline end to end (video +
audio). The same UI is the foundation for the RTMP / RTSP ingest and HLS egress
scenarios tracked in the [Demo UI Improvements milestone][milestone].

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

| Scenario | What it exercises                                  | Status            |
| -------- | -------------------------------------------------- | ----------------- |
| Echo     | Publish → relay → subscribe (MoQ-MoQ, this demo)   | Working           |
| RTMP     | Subscribe to an RTMP-ingested stream (`/live/demo`) | Planned (#141)    |
| RTSP     | Subscribe to an RTSP-ingested stream                | Planned (#141)    |
| HLS      | Consume the relay's HLS egress                      | Blocked (#142)    |

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
  Dashboard.tsx        Top-level controls, connection status, board layout
  ConnectionStatus.tsx WebTransport lifecycle indicator (issue #134)
  publish/             Publish board: capture → encode → MoQ
  subscribe/           Subscribe board: MoQ → decode → canvas
  user/                Random-username session identity
```
