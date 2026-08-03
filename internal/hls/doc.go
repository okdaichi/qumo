// Package hls serves a MoQ track as HLS and DASH by feeding it through
// qumo-ledger.
//
// [Run] connects to a MoQ relay, subscribes to a track, and writes each
// received group into a qumo-ledger track as a sealed group; the ledger's
// [stream.Handler] renders that track as an HLS playlist and a DASH MPD over
// HTTP. The feed is the qumo (relay) side; the rendering is the qumo-ledger
// (storage) side, so the two stay decoupled.
//
// This is an in-progress feature. Two pieces are deliberately approximate:
//
//   - The group payload is the raw concatenation of MoQ frame bodies, not yet
//     packaged as fMP4 segments, so the segments are not yet HLS-playable.
//   - gomoqt v0.15.0 exposes no per-frame media timestamp, so a group's
//     MediaTime and Duration are derived from a configured group duration and
//     the wall clock rather than measured from the stream.
//
// Both are flagged TODOs for the feature work.
package hls
