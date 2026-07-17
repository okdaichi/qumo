//go:build integration

// Single-relay fan-out capacity benchmark.
//
// Topology: publisher → [relay] → K subscribers (direct, 1-hop).
//
// This is the canonical single-node capacity benchmark. The 2-hop relay-chain
// benchmark (BenchmarkRelayChain_FanoutSweep) tests origin→leaf→subscriber
// and is a separate investigation; this benchmark isolates one relay instance's
// fan-out ceiling without multi-relay topology effects.
//
// The knee is at K≈32–48 on an 8C/16T i7-10700K (500fps, 1200B frames, 10s).
// Degradation is smooth (no cliff, no spiral): loss rises gradually, latency
// grows linearly, heap stays flat (6–8MB).

package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/require"
)

// BenchmarkRelayChain_FanoutSingleRelay sweeps K for a single relay instance.
// Override K with FANOUT_KS, duration with BENCH_DURATION.
func BenchmarkRelayChain_FanoutSingleRelay(b *testing.B) {
	cert, pool := chainCert(b)
	quicCfg := &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, MaxIncomingUniStreams: 1 << 20, MaxIncomingStreams: 1 << 20}

	dur := 3 * time.Second
	if d := os.Getenv("BENCH_DURATION"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			dur = parsed
		}
	}
	const sz = 1200
	// groupInterval: gap BETWEEN groups. Each MoQ group opens a fresh QUIC
	// uni-stream, so this is the stream open/close rate per subscriber. 2ms = 500
	// groups/s (the original pathological stress workload). Real video ≈ 1–5/s.
	groupInterval := 2 * time.Millisecond
	if v := envIntDef("GROUP_INTERVAL_MS", 0); v > 0 {
		groupInterval = time.Duration(v) * time.Millisecond
	}
	// framesPerGroup + frameGap hold byte throughput constant while varying
	// stream churn: 500 groups/s×1frame and 10 groups/s×50frames both move
	// ~500 frames/s, but the latter opens 50× fewer streams.
	framesPerGroup := 1
	if v := envIntDef("FRAMES_PER_GROUP", 0); v > 0 {
		framesPerGroup = v
	}
	frameGap := time.Duration(0)
	if v := envIntDef("FRAME_GAP_MS", 0); v > 0 {
		frameGap = time.Duration(v) * time.Millisecond
	}

	ks := parseIntListEnv("FANOUT_KS", []int{1, 2, 4, 8, 16, 32, 64})
	log.Printf("\n=== Single-Relay Fan-out (groupInterval=%s, frames/group=%d, frameGap=%s, size=%dB, dur=%s, K=%v) ===",
		groupInterval, framesPerGroup, frameGap, sz, dur, ks)
	log.Printf("%-6s %-8s %-8s %-8s %-8s %-10s %-8s %-6s", "K", "med", "p95", "p99", "loss%", "fps", "heapMB", "goros")

	for _, K := range ks {
		b.Run(fmt.Sprintf("K=%d", K), func(b *testing.B) {
			st := singleRelayFanoutRun(b, cert, pool, quicCfg, K, sz, groupInterval, framesPerGroup, frameGap, dur)
			b.ReportMetric(st.median.Seconds()*1000, "med_ms")
			b.ReportMetric(st.p99.Seconds()*1000, "p99_ms")
			b.ReportMetric(st.lossPct, "loss%")
			b.ReportMetric(st.fps, "fps")
			b.ReportMetric(st.heapMB, "heapMB")
			log.Printf("%-6d %-8s %-8s %-8s %-8.2f %-10.0f %-8.2f %-6d",
				K, st.median.Round(time.Microsecond), st.p95.Round(time.Microsecond),
				st.p99.Round(time.Microsecond), st.lossPct, st.fps, st.heapMB, st.goros)
		})
	}
}

// singleRelayFanoutRun: one publisher → one relay → K direct subscribers.
func singleRelayFanoutRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, quicCfg *quic.Config, K int, frameSize int, groupInterval time.Duration, framesPerGroup int, frameGap, duration time.Duration) scalabilityStats {
	tb.Helper()
	if v := envIntDef("RELAY_NOTIFY_TIMEOUT_MS", 0); v > 0 {
		NotifyTimeout = time.Duration(v) * time.Millisecond
	}

	relay := spinRelay(tb, "relay", chainFreeAddr(tb), cert, pool, quicCfg)
	relayAddr := relay.MOQServer.Addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, duration)
	defer runCancel()

	var sentCounter uint64
	var pub publisherTimings
	pubDone := make(chan struct{})
	pubMux := moqt.NewTrackMux(moqt.NewHopID())
	pubMux.PublishFunc(runCtx, chainBroadcastPath, func(tw *moqt.TrackWriter) {
		defer close(pubDone)
		defer tw.Close()
		payload := make([]byte, frameSize)
		for {
			if runCtx.Err() != nil {
				return
			}
			// Time the publisher-side group/stream open. OpenGroup blocks on the
			// peer's MAX_STREAMS; a large value means the relay isn't granting
			// streams (unlikely here — relay sets MaxIncomingUniStreams=1<<20).
			tOpen := time.Now()
			gw, err := tw.OpenGroup(runCtx)
			if err != nil {
				return
			}
			pub.openGroup = append(pub.openGroup, time.Since(tOpen))
			for i := 0; i < framesPerGroup; i++ {
				if runCtx.Err() != nil {
					_ = gw.Close()
					return
				}
				binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
				fr := moqt.NewFrame(frameSize)
				_, _ = fr.Write(payload)
				// Time WriteFrame (= stream.Write). If this blocks into the ms
				// range, the relay's receive side is reading slowly and QUIC flow
				// control is backpressuring the publisher — i.e. the publisher→relay
				// latency is sender-side blocking, not downstream transport.
				tW := time.Now()
				_ = gw.WriteFrame(fr)
				pub.writeFrame = append(pub.writeFrame, time.Since(tW))
				atomic.AddUint64(&sentCounter, 1) // per frame: keeps loss = frame-loss (recv counts frames)
				if frameGap > 0 {
					time.Sleep(frameGap)
				}
			}
			tClose := time.Now()
			_ = gw.Close()
			pub.closeGroup = append(pub.closeGroup, time.Since(tClose))
			if groupInterval > 0 {
				time.Sleep(groupInterval)
			}
		}
	})

	pubSess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: &quic.Config{EnableDatagrams: true, MaxIncomingUniStreams: 1 << 20, MaxIncomingStreams: 1 << 20}}).Dial(runCtx, "moqt://"+relayAddr, pubMux)
	require.NoError(tb, err)
	defer pubSess.CloseWithError(moqt.NoError, "done")

	// Wait for the handler to register.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ann, _ := relay.TrackMux.TrackHandler(chainBroadcastPath); ann != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	before := snapshotBefore()
	results := make([][]time.Duration, K)
	var wg sync.WaitGroup
	for i := range K {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = subscribeAndRead(tb, relayAddr, pool, duration+5*time.Second)
		}()
	}
	wg.Wait()
	after := snapshotBefore()
	_ = pubSess.CloseWithError(moqt.NoError, "done")

	var allLats []time.Duration
	totalRecv := 0
	perSubRecv := make([]float64, K)
	for i, lats := range results {
		allLats = append(allLats, lats...)
		totalRecv += len(lats)
		perSubRecv[i] = float64(len(lats))
	}
	sent := atomic.LoadUint64(&sentCounter)
	avgRecv := totalRecv / K
	lossPct := 0.0
	if sent > 0 {
		lossPct = (float64(sent) - float64(avgRecv)) / float64(sent) * 100
	}
	fps := float64(avgRecv) / duration.Seconds()
	heapMB, goros, cpu := before.delta(after)

	var sum, sumSq float64
	for _, r := range perSubRecv {
		sum += r
		sumSq += r * r
	}
	fairness := 1.0
	if sumSq > 0 {
		fairness = (sum * sum) / (float64(K) * sumSq)
	}

	st := scalabilityStats{
		K:      K,
		min:    percentile(allLats, 0), p25: percentile(allLats, 25),
		median: percentile(allLats, 50), p75: percentile(allLats, 75),
		p95: percentile(allLats, 95), p99: percentile(allLats, 99), maxLat: percentile(allLats, 100),
		lossPct: lossPct, fps: fps, mbps: fps * float64(frameSize) * 8 / 1e6,
		heapMB: heapMB, goros: goros, cpuMs: cpu.Seconds() * 1000,
		fairness: fairness,
	}
	// Per-stage latency decomposition (no-op/empty in the default build; rich
	// under -tags instrument). EndToEnd is the payload-timestamp latency (allLats);
	// Residual isolates the quic-go sendQueue→syscall drain.
	logStageLatency(tb, relay, StageSnapshot{
		N: len(allLats), P50: st.median, P95: st.p95, P99: st.p99, Max: st.maxLat,
	})
	// Publisher-side blocking report. Wait for the publisher goroutine to finish
	// (it returns when runCtx expires at `duration`) so pub is safe to read.
	select {
	case <-pubDone:
	case <-time.After(duration + 3 * time.Second):
	}
	reportPublisherTimings(tb, pub)
	return st
}

// subscribeAndRead dials a relay, subscribes, reads groups until timeout,
// returns per-group latencies.
func subscribeAndRead(tb testing.TB, addr string, pool *x509.CertPool, timeout time.Duration) []time.Duration {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: &quic.Config{EnableDatagrams: true, MaxIncomingUniStreams: 1 << 20, MaxIncomingStreams: 1 << 20}}).Dial(ctx, "moqt://"+addr, moqt.NewTrackMux(0))
	if err != nil {
		return nil
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	tr, err := sess.Subscribe(ctx, chainBroadcastPath, chainTrackName, nil)
	if err != nil {
		return nil
	}
	defer tr.Close()
	buf := moqt.NewFrame(1200 + 256)
	var lats []time.Duration
	for {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			break
		}
		for frame := range gr.Frames(buf) {
			body := frame.Body()
			if len(body) >= chainFrameHeader {
				pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
				lats = append(lats, time.Since(time.Unix(0, pubNs)))
			}
		}
	}
	return lats
}

// logStageLatency prints the per-stage latency decomposition from the relay's
// collector. Prints nothing in the default build (StageLatency returns nil); rich
// under -tags instrument. EndToEnd (e2e) is the payload-embedded publish→read
// latency; Residual = EndToEnd.P50 − the stage P50s, isolating the quic-go
// sendQueue→syscall drain (plus wire + subscriber read, ~0 on loopback).
func logStageLatency(tb testing.TB, relay *Server, e2e StageSnapshot) {
	tb.Helper()
	r := relay.StageLatency()
	if r == nil {
		return
	}
	r.EndToEnd = e2e
	r.Residual = e2e.P50 - (r.Transit.P50 + r.Ingress.P50 + r.Residence.P50 + r.Egress.P50 + r.Enqueue.P50)

	type row struct {
		name string
		s    StageSnapshot
	}
	rows := []row{
		{"transit   pub->relay", r.Transit},
		{"ingress(A) clone+publish", r.Ingress},
		{"resid(R)  ring->egress", r.Residence},
		{"egress(C) WriteFrame", r.Egress},
		{"enqueue(D) quic-go", r.Enqueue},
		{"end2end   publish->read", r.EndToEnd},
	}
	log.Printf("[stages] per-stage latency (auto ns/µs/ms):")
	for _, rr := range rows {
		log.Printf("[stages]   %-26s n=%-8d p50=%-12v p95=%-12v p99=%-12v max=%-12v",
			rr.name, rr.s.N, rr.s.P50, rr.s.P95, rr.s.P99, rr.s.Max)
	}
	log.Printf("[stages]   %-26s          p50=%v   [= e2e.p50 - sum(stage p50); the sendQueue->syscall drain]",
		"residual", r.Residual)
}

// publisherTimings records publisher-side OpenGroup/WriteFrame/Close durations —
// the sender-side backpressure signal that splits the publisher→relay transit
// into "publisher blocking" vs "downstream transport/relay-receive". Single
// publisher goroutine; only read after pubDone is closed.
type publisherTimings struct {
	openGroup  []time.Duration
	writeFrame []time.Duration
	closeGroup []time.Duration
}

// reportPublisherTimings prints the publisher-side blocking distribution. A large
// WriteFrame p50/p99 means the relay receive side is reading slowly and QUIC flow
// control is backpressuring the publisher (sender-side blocking). A small
// WriteFrame with large transit means the delay is downstream of the hand-off.
func reportPublisherTimings(tb testing.TB, pub publisherTimings) {
	tb.Helper()
	if len(pub.writeFrame) == 0 {
		return
	}
	log.Printf("[pub] publisher-side blocking (sender backpressure):")
	log.Printf("[pub]   %-22s n=%-8d p50=%-12v p95=%-12v p99=%-12v max=%-12v",
		"OpenGroup(MAX_STREAMS)", len(pub.openGroup),
		percentile(pub.openGroup, 50), percentile(pub.openGroup, 95), percentile(pub.openGroup, 99), percentile(pub.openGroup, 100))
	log.Printf("[pub]   %-22s n=%-8d p50=%-12v p95=%-12v p99=%-12v max=%-12v",
		"WriteFrame(flow ctrl)", len(pub.writeFrame),
		percentile(pub.writeFrame, 50), percentile(pub.writeFrame, 95), percentile(pub.writeFrame, 99), percentile(pub.writeFrame, 100))
	log.Printf("[pub]   %-22s n=%-8d p50=%-12v p95=%-12v p99=%-12v max=%-12v",
		"Close", len(pub.closeGroup),
		percentile(pub.closeGroup, 50), percentile(pub.closeGroup, 95), percentile(pub.closeGroup, 99), percentile(pub.closeGroup, 100))
}
