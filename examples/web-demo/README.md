# Web Demo

A simple web application to test the qumo MoQ relay server.

## Quick Start

```bash
# Start relay server and web demo
mage web

# Open browser at http://localhost:5173
```

## Features

- 📹 Publish video from webcam
- 🎤 Publish audio from microphone
- 📡 Subscribe to remote streams
- 🔄 Real-time streaming via MoQ protocol

## Development

```bash
# Development mode with hot reload
mage web:dev

# Build for production
mage web:build

# Clean build artifacts
mage web:clean

# or use: mage -d ./magefiles <target>
```

## Stack

- **Frontend**: Vite + TypeScript
- **Protocol**: [@qumo/moq](https://jsr.io/@qumo/moq) from JSR
- **Relay**: qumo (QUIC/MoQ relay server)

## Architecture

```
┌─────────────┐         QUIC/MoQ          ┌─────────────┐
│  Publisher  │ ───────────────────────> │             │
│  (Browser)  │                           │    Relay    │
└─────────────┘                           │   Server    │
                                          │   (qumo)    │
┌─────────────┐         QUIC/MoQ          │             │
│ Subscriber  │ <─────────────────────── │             │
│  (Browser)  │                           └─────────────┘
└─────────────┘
```
