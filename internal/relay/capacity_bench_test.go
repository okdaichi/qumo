//go:build integration

// Single-node CAPACITY benchmarks — GPS-driven. See BENCHMARKING.md.
//
// Terminology (precise):
//   - Session = one QUIC/WebTransport connection.
//   - Track   = a subscription within a session.
//   - Group   = one MoQ Group (= one QUIC uni-stream; "stream churn" = Groups/sec).
//   - GPS     = Groups created per second within one Track in one Session.
//
// FPS is DERIVED: FPS = GPS × FramesPerGroup. It is NOT an input. The independent
// axes are: concurrent Sessions (the primary scaling variable), GPS, FramesPerGroup,
// FrameSize. The first-order finding driving this design: at matched packet rate,
// GPS (stream churn) binds well before FPS — so the relay is characterized by its
// GPS ceiling, its Sessions ceiling, its PPS (socket) ceiling, and its bandwidth
// ceiling, each isolated by a dedicated probe (see BENCHMARKING.md §matrix).
//
// Each benchmark is ONE cell (single-Sessions-per-process). Sweep externally.
//
// CAVEAT: WSL2 is a loaded VM (±10× swing, no GSO verification); valid for SHAPE
// and which-axis-binds, not absolute ceilings. Rerun on bare-metal Linux for those.

package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"log"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/require"
)

// ---- env helpers (envIntDef lives in relay_chain_scalability_test.go) ----

func envDurDef(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envFloatDef(name string, def float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// snapshotResources returns current heap (MB) and goroutine count, used to measure
// steady-state hold cost while Sessions are active (not a before/after delta —
// subscribers close before a post-run snapshot).
func snapshotResources() (rssMB float64, goros int) {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1024 * 1024), runtime.NumGoroutine()
}

// waitRamp blocks until count reaches target or the deadline elapses.
func waitRamp(count *atomic.Int64, target int, deadline time.Duration) {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) && int(count.Load()) < target {
		time.Sleep(50 * time.Millisecond)
	}
}

// capacityQuicCfg MATCHES PRODUCTION (cmd.go): MaxIncomingUniStreams/Streams left
// at quic-go defaults (~100) so OpenGroupAt backpressure engages as real clients.
// (The benches previously set 1<<20, defeating backpressure → unbounded retention
// → GC spiral. EnableDatagrams is forced true by gomoqt for WebTransport anyway.)
func capacityQuicCfg() *quic.Config {
	return &quic.Config{
		EnableDatagrams: true,
		KeepAlivePeriod: 5 * time.Second,
		MaxIdleTimeout:  30 * time.Second,
	}
}

// ============================================================
// BenchmarkRelay_CapacityFrontier — ONE GPS-driven cell.
// ============================================================

// BenchmarkRelay_CapacityFrontier measures one (Sessions, GPS, FramesPerGroup,
// FrameSize) cell. The publisher opens a Group every 1/GPS seconds and writes
// FramesPerGroup frames into it (so offered FPS = GPS × FramesPerGroup, derived).
// If the relay backpressures, groups are missed and the delivery ratio falls below
// 1 — which loss% (denominated on actual-written) cannot see.
//
// Env: SESSIONS, GPS (float, e.g. 0.5/1/10/100), FRAMES_PER_GROUP, FRAME_SIZE,
// BENCH_DURATION, SUSTAIN_P99_MS (τ), SUSTAIN_DELIVERY_RATIO.
func BenchmarkRelay_CapacityFrontier(b *testing.B) {
	cert, pool := chainCert(b)
	quicCfg := capacityQuicCfg()

	sessions := envIntDef("SESSIONS", 64)
	gps := envFloatDef("GPS", 10)
	framesPerGroup := envIntDef("FRAMES_PER_GROUP", 1)
	if framesPerGroup < 1 {
		framesPerGroup = 1
	}
	size := envIntDef("FRAME_SIZE", 1200)
	dur := envDurDef("BENCH_DURATION", 8*time.Second)
	p99Threshold := time.Duration(envIntDef("SUSTAIN_P99_MS", 250)) * time.Millisecond
	deliveryFloor := envFloatDef("SUSTAIN_DELIVERY_RATIO", 0.95)

	r := capacityFrontierRun(b, cert, pool, quicCfg, sessions, gps, framesPerGroup, size, dur)

	verdict := "NOT-SUSTAINED"
	if r.deliveryRatio >= deliveryFloor && r.p99 <= p99Threshold {
		verdict = "SUSTAINED"
	}
	offeredFPS := gps * float64(framesPerGroup)
	b.ReportMetric(r.deliveredAggGPS, "agg_gps")
	b.ReportMetric(r.deliveredGPSPerSession, "gps_per_session")
	b.ReportMetric(r.deliveredAggFPS, "agg_fps")
	b.ReportMetric(r.deliveredAggBwMbps, "agg_mbps")
	b.ReportMetric(r.groupWriteRatio, "write_ratio")
	b.ReportMetric(r.deliveryRatio, "deliv_ratio")
	b.ReportMetric(r.p99.Seconds()*1000, "p99_ms")
	b.ReportMetric(float64(r.goros), "goros")
	log.Printf("[frontier] S=%-5d GPS=%-5g F=%-3d size=%-5dB | offered gps/sess=%-5g (fps=%-7.1f) | deliv gps/sess=%-6.2f agg_gps=%-8.0f agg_fps=%-9.0f agg=%-7.1fMbps write=%-.3f ratio=%-.3f p99=%-9s rssΔ=%-5.1fMB gorosΔ=%-6d => %s",
		sessions, gps, framesPerGroup, size, gps, offeredFPS,
		r.deliveredGPSPerSession, r.deliveredAggGPS, r.deliveredAggFPS, r.deliveredAggBwMbps,
		r.groupWriteRatio, r.deliveryRatio, r.p99.Round(time.Microsecond), r.rssMB, r.goros, verdict)
}

type capacityResult struct {
	deliveredGPSPerSession float64 // Groups/sec received per session
	deliveredAggGPS        float64 // aggregate Groups/sec across all sessions
	deliveredFPSPerSession float64 // frames/sec received per session (= GPS×F at full fidelity)
	deliveredAggFPS        float64 // aggregate FRAMES/sec (≈ packets/sec only for ≤MTU frames; a 16KB frame is ~14 packets)
	deliveredAggBwMbps     float64 // aggregate bandwidth
	groupWriteRatio        float64 // groups the publisher got through / offered (<1 = publisher backpressured)
	deliveryRatio          float64 // delivered frames / offered frames (1.0 = full fidelity)
	p99                    time.Duration
	rssMB                  float64
	goros                  int
}

func capacityFrontierRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, quicCfg *quic.Config, sessions int, gps float64, framesPerGroup, size int, dur time.Duration) capacityResult {
	tb.Helper()
	relay := spinRelay(tb, "relay", chainFreeAddr(tb), cert, pool, quicCfg)
	relayAddr := relay.MOQServer.Addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, dur)
	defer runCancel()

	// Publisher: GPS-driven. Opens a Group every 1/GPS seconds, writes
	// FramesPerGroup frames, closes. Offered GPS = gps; offered FPS = gps×F.
	// If OpenGroup/WriteFrame block, the group tick is missed → delivery ratio <1.
	var groupsWritten atomic.Uint64
	groupInterval := time.Duration(float64(time.Second) / gps)
	pubMux := moqt.NewTrackMux(moqt.NewHopID())
	pubMux.PublishFunc(runCtx, chainBroadcastPath, func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, size)
		ticker := time.NewTicker(groupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				gw, err := tw.OpenGroup(runCtx) // blocks on peer MAX_STREAMS
				if err != nil || gw == nil {
					continue // missed group (backpressure) — not credited
				}
				groupsWritten.Add(1)
				for f := 0; f < framesPerGroup; f++ {
					if runCtx.Err() != nil {
						_ = gw.Close()
						return
					}
					binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
					fr := moqt.NewFrame(size)
					_, _ = fr.Write(payload)
					_ = gw.WriteFrame(fr)
				}
				_ = gw.Close()
			}
		}
	})
	pubSess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(runCtx, "moqt://"+relayAddr, pubMux)
	require.NoError(tb, err)
	defer pubSess.CloseWithError(moqt.NoError, "done")

	waitForHandler(tb, relay, chainBroadcastPath)
	baseRSS, baseGoros := snapshotResources()

	type subResult struct{ groups, frames int }
	results := make([]subResult, sessions)
	var allLats []time.Duration
	var latMu sync.Mutex
	var connected atomic.Int64
	var wg sync.WaitGroup
	for i := range sessions {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, f, lats := capacitySubscribe(tb, relayAddr, pool, quicCfg, dur+5*time.Second, &connected)
			results[i] = subResult{g, f}
			latMu.Lock()
			allLats = append(allLats, lats...)
			latMu.Unlock()
		}()
	}
	waitRamp(&connected, sessions, min(dur/2, 10*time.Second))
	steadyRSS, steadyGoros := snapshotResources()
	wg.Wait()

	totalGroups, totalFrames := 0, 0
	for _, r := range results {
		totalGroups += r.groups
		totalFrames += r.frames
	}
	secs := dur.Seconds()
	deliveredGPSPerSession := float64(totalGroups) / float64(sessions) / secs
	deliveredFPSPerSession := float64(totalFrames) / float64(sessions) / secs
	offeredFPSPerSession := gps * float64(framesPerGroup)
	deliveryRatio := 0.0
	if offeredFPSPerSession > 0 {
		deliveryRatio = deliveredFPSPerSession / offeredFPSPerSession
	}
	// groupWriteRatio = groups the publisher got through / offered. If <1 the
	// publisher was backpressured (OpenGroup/WriteFrame blocked); if ≈1 but
	// deliveryRatio <1 the relay itself dropped groups. Distinguishes the two.
	offeredGroups := gps * secs
	groupWriteRatio := 0.0
	if offeredGroups > 0 {
		groupWriteRatio = float64(groupsWritten.Load()) / offeredGroups
	}
	return capacityResult{
		deliveredGPSPerSession: deliveredGPSPerSession,
		deliveredAggGPS:        float64(totalGroups) / secs,
		deliveredFPSPerSession: deliveredFPSPerSession,
		deliveredAggFPS:        float64(totalFrames) / secs,
		deliveredAggBwMbps:     float64(totalFrames) / secs * float64(size) * 8 / 1e6,
		groupWriteRatio:        groupWriteRatio,
		deliveryRatio:          deliveryRatio,
		p99:                    percentile(allLats, 99),
		rssMB:                  steadyRSS - baseRSS,
		goros:                  steadyGoros - baseGoros,
	}
}

// capacitySubscribe dials one subscriber Session, subscribes (1 Track), and reads
// until timeout, returning Group count, frame count, and per-frame end-to-end
// latencies (from the payload publish timestamp).
func capacitySubscribe(tb testing.TB, addr string, pool *x509.CertPool, quicCfg *quic.Config, timeout time.Duration, connected *atomic.Int64) (int, int, []time.Duration) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(ctx, "moqt://"+addr, moqt.NewTrackMux(0))
	if err != nil {
		return 0, 0, nil
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	tr, err := sess.Subscribe(ctx, chainBroadcastPath, chainTrackName, nil)
	if err != nil {
		return 0, 0, nil
	}
	connected.Add(1)
	defer tr.Close()
	buf := moqt.NewFrame(1200 + 256)
	var groups, frames int
	var lats []time.Duration
	for {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			break
		}
		groups++
		for frame := range gr.Frames(buf) {
			body := frame.Body()
			if len(body) >= chainFrameHeader {
				pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
				lats = append(lats, time.Since(time.Unix(0, pubNs)))
			}
			frames++
		}
	}
	return groups, frames, lats
}

// ============================================================
// BenchmarkRelay_ConnectionCarry — Sessions scalability (axis 1).
// ============================================================

// BenchmarkRelay_ConnectionCarry holds SESSIONS subscriber Sessions open against a
// minimal trickle (CARRY_GPS groups/s, small frames) to test the connection-
// carrying / per-Session overhead axis in isolation. Delivery is far below any
// ceiling so failure is per-Session state/goroutine saturation, not delivery.
//
// Env: SESSIONS, BENCH_DURATION, CARRY_GPS (default 0.5), CARRY_SIZE (default 64).
func BenchmarkRelay_ConnectionCarry(b *testing.B) {
	cert, pool := chainCert(b)
	quicCfg := capacityQuicCfg()

	sessions := envIntDef("SESSIONS", 1000)
	dur := envDurDef("BENCH_DURATION", 10*time.Second)
	gps := envFloatDef("CARRY_GPS", 0.5)
	size := envIntDef("CARRY_SIZE", 64)
	if size < 16 {
		size = 16
	}
	// rampRate=0 → burst (Establishment ceiling). >0 → controlled ramp (Hold ceiling).
	rampRate := envFloatDef("RAMP_SESSIONS_PER_SEC", 0)

	r := connectionCarryRun(b, cert, pool, quicCfg, sessions, gps, size, dur, rampRate)

	mode := "burst-establishment"
	if rampRate > 0 {
		mode = "slow-ramp-hold"
	}
	verdict := "HOLDS"
	if r.receiving < int(float64(sessions)*0.99) {
		verdict = "CANNOT-HOLD"
	}
	perConnKB := 0.0
	if r.connected > 0 {
		perConnKB = r.rssMB * 1024 / float64(r.connected)
	}
	b.ReportMetric(float64(r.connected), "connected")
	b.ReportMetric(float64(r.receiving), "receiving")
	b.ReportMetric(r.rssMB, "rss_mb")
	b.ReportMetric(float64(r.goros), "goros")
	log.Printf("[carry] mode=%-20s S=%-6d ramp=%-4g/s | connected=%-6d receiving=%-6d rssΔ=%-6.1fMB gorosΔ=%-6d perSession=%-.1fKB => %s",
		mode, sessions, rampRate, r.connected, r.receiving, r.rssMB, r.goros, perConnKB, verdict)
	// Machine-readable emission for the consolidated bench dashboard (no-op unless
	// BENCH_RESULTS_DIR is set). The capacity group carries the session-axis ceiling.
	recordBench(b, benchResult{
		Bench: "ConnectionCarry", Group: "capacity", Config: mode + "/S=" + strconv.Itoa(sessions),
		Sessions: sessions, Connected: r.connected, Receiving: r.receiving,
		HeapMB: r.rssMB, Goros: r.goros, PerSessionKB: perConnKB, Verdict: verdict,
	})
}

type carryResult struct {
	connected int
	receiving int
	rssMB     float64
	goros     int
}

func connectionCarryRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, quicCfg *quic.Config, sessions int, gps float64, size int, dur time.Duration, rampRate float64) carryResult {
	tb.Helper()
	relay := spinRelay(tb, "relay", chainFreeAddr(tb), cert, pool, quicCfg)
	relayAddr := relay.MOQServer.Addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, dur)
	defer runCancel()

	groupInterval := time.Duration(float64(time.Second) / gps)
	pubMux := moqt.NewTrackMux(moqt.NewHopID())
	pubMux.PublishFunc(runCtx, chainBroadcastPath, func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, size)
		ticker := time.NewTicker(groupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
				gw, err := tw.OpenGroup(runCtx)
				if err != nil || gw == nil {
					continue
				}
				fr := moqt.NewFrame(size)
				_, _ = fr.Write(payload)
				_ = gw.WriteFrame(fr)
				_ = gw.Close()
			}
		}
	})
	pubSess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(runCtx, "moqt://"+relayAddr, pubMux)
	require.NoError(tb, err)
	defer pubSess.CloseWithError(moqt.NoError, "done")

	waitForHandler(tb, relay, chainBroadcastPath)
	baseRSS, baseGoros := snapshotResources()

	connected := make([]bool, sessions)
	receiving := make([]int, sessions)
	var connCount atomic.Int64
	var wg sync.WaitGroup
	launch := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, n := carrySubscribe(tb, relayAddr, pool, quicCfg, dur+10*time.Second, &connCount)
			connected[i] = ok
			receiving[i] = n
		}()
	}
	// Establishment mode. rampRate>0 launches Sessions at a controlled rate,
	// measuring the Steady-State HOLD ceiling (gradual establishment); rampRate=0
	// launches all at once, measuring the burst ESTABLISHMENT ceiling. These are
	// distinct architectural properties — a node may fail to burst-connect 10K
	// while still holding 10K established gradually.
	rampDeadline := min(dur, 30*time.Second)
	if rampRate > 0 {
		interval := time.Duration(float64(time.Second) / rampRate)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
	rampLoop:
		for i := 0; i < sessions; i++ {
			select {
			case <-runCtx.Done():
				break rampLoop
			case <-ticker.C:
				launch(i)
			}
		}
		rampDeadline = time.Duration(float64(sessions)/rampRate*float64(time.Second)) + 10*time.Second
	} else {
		for i := range sessions {
			launch(i)
		}
	}
	waitRamp(&connCount, sessions, rampDeadline)
	steadyRSS, steadyGoros := snapshotResources()
	wg.Wait()

	conn, recv := 0, 0
	for i := range sessions {
		if connected[i] {
			conn++
		}
		if receiving[i] > 0 {
			recv++
		}
	}
	return carryResult{connected: conn, receiving: recv, rssMB: steadyRSS - baseRSS, goros: steadyGoros - baseGoros}
}

func carrySubscribe(tb testing.TB, addr string, pool *x509.CertPool, quicCfg *quic.Config, timeout time.Duration, connected *atomic.Int64) (bool, int) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(ctx, "moqt://"+addr, moqt.NewTrackMux(0))
	if err != nil {
		return false, 0
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	tr, err := sess.Subscribe(ctx, chainBroadcastPath, chainTrackName, nil)
	if err != nil {
		return false, 0
	}
	connected.Add(1)
	defer tr.Close()
	buf := moqt.NewFrame(1200 + 256)
	var n int
	for {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			break
		}
		for range gr.Frames(buf) {
			n++
		}
	}
	return true, n
}
