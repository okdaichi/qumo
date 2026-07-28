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
	"strconv"
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
	gap := 2 * time.Millisecond
	if g := os.Getenv("FANOUT_GAP"); g != "" {
		if parsed, err := time.ParseDuration(g); err == nil {
			gap = parsed
		}
	}
	const sz = 1200

	ks := parseIntListEnv("FANOUT_KS", []int{1, 2, 4, 8, 16, 32, 64})
	log.Printf("\n=== Single-Relay Fan-out (gap=%s, size=%dB, dur=%s, K=%v) ===", gap, sz, dur, ks)
	log.Printf("%-6s %-8s %-8s %-8s %-8s %-10s %-8s %-6s", "K", "med", "p95", "p99", "loss%", "fps", "heapMB", "goros")

	for _, K := range ks {
		b.Run(fmt.Sprintf("K=%d", K), func(b *testing.B) {
			st := singleRelayFanoutRun(b, cert, pool, quicCfg, K, sz, gap, dur)
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
func singleRelayFanoutRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, quicCfg *quic.Config, K int, frameSize int, gap, duration time.Duration) scalabilityStats {
	tb.Helper()

	relay := spinRelay(tb, "relay", chainFreeAddr(tb), cert, pool, quicCfg)
	relayAddr := relay.MOQServer.Addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, duration)
	defer runCancel()

	// framesPerGroup models the Group/Frame ratio. 1 = one group per frame
	// (the group-lifecycle stress test); >1 = a GOP-style group carrying N
	// frames on one stream (realistic video: FANOUT_FPG=30 → 1s GOP at 30fps,
	// 60 → 2s GOP). Intra-group pacing stays at `gap` so the frame rate is
	// unchanged; only the group-open (stream-open) rate changes (30/FPG per sec).
	framesPerGroup := 1
	if v := os.Getenv("FANOUT_FPG"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			framesPerGroup = n
		}
	}

	var sentCounter uint64
	pubMux := moqt.NewTrackMux(moqt.NewHopID())
	pubMux.PublishFunc(runCtx, chainBroadcastPath, func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, frameSize)
		for {
			if runCtx.Err() != nil {
				return
			}
			gw, err := tw.OpenGroup(runCtx)
			if err != nil {
				return
			}
			for f := 0; f < framesPerGroup; f++ {
				if runCtx.Err() != nil {
					break
				}
				atomic.AddUint64(&sentCounter, 1)
				binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
				fr := moqt.NewFrame(frameSize)
				_, _ = fr.Write(payload)
				if gw.WriteFrame(fr) != nil {
					break
				}
				if gap > 0 {
					time.Sleep(gap) // maintain the per-frame rate within the group
				}
			}
			_ = gw.Close()
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

	// Settle window: stage histograms are reset and e2e samples discarded until
	// settleAt, so percentiles reflect steady state, not connection ramp-up.
	// sentAtSettle snapshots the publish counter at the same instant so loss%
	// and fps are computed over the steady-state window only — without it, the
	// dropped ramp-up samples would masquerade as ~settle/duration loss.
	settle := duration / 4
	if settle > 5*time.Second {
		settle = 5 * time.Second
	}
	settleAt := time.Now().Add(settle)
	var sentAtSettle uint64
	time.AfterFunc(settle, func() {
		atomic.StoreUint64(&sentAtSettle, atomic.LoadUint64(&sentCounter))
		relay.StageLatencyReset()
	})

	before := snapshotBefore()
	results := make([][]time.Duration, K)
	var wg sync.WaitGroup
	for i := range K {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = subscribeAndRead(tb, relayAddr, pool, duration+5*time.Second, settleAt)
		}()
	}
	wg.Wait()
	after := snapshotBefore()
	_ = pubSess.CloseWithError(moqt.NoError, "done")

	if rep := relay.StageLatency(); rep != nil {
		logStage := func(name string, s StageSnapshot) {
			log.Printf("stage %-14s n=%-9d p50=%-10s p95=%-10s p99=%-10s max=%s",
				name, s.N, s.P50, s.P95, s.P99, s.Max)
		}
		log.Printf("--- stage latency (K=%d, steady-state after %s settle) ---", K, settle)
		logStage("A ingress", rep.IngressService)
		logStage("R ring-wait", rep.RingResidence)
		logStage("O group-open", rep.GroupOpen)
		logStage("C write-frame", rep.EgressService)
		log.Printf("--- MECHANISM (A: serialization vs B: shared-resource) K=%d ---", K)
		logStage("R.fill  (reserve->bcast)", rep.RingFill)
		logStage("R.wake  (bcast->pickup)", rep.RingWake)
		logStage("R.behind(direct pickup)", rep.RingBehind)
		logStage("R.woken (after wait)", rep.RingWoken)
		logStage("deliverSpan(entry->end)", rep.DeliverSpan)
		logStage("broadcast dur", rep.BroadcastDur)
		logStage("fillSem wait", rep.FillSemWait)
		logStage("GROUP inter-arrival", rep.GroupInterArrival)
		log.Printf("mechanism gauges: maxConcurrentGroups=%d maxConcurrentDeliveries=%d broadcasts=%d subs=%d",
			rep.MaxConcurrentGroups, rep.MaxConcurrentDeliveries, rep.BroadcastN, K)
	}

	var allLats []time.Duration
	totalRecv := 0
	perSubRecv := make([]float64, K)
	for i, lats := range results {
		allLats = append(allLats, lats...)
		totalRecv += len(lats)
		perSubRecv[i] = float64(len(lats))
	}
	// Steady-state accounting: frames sent before the settle snapshot were
	// deliberately discarded by subscribeAndRead, so only post-settle sends
	// count toward loss and fps (see sentAtSettle).
	steadySent := atomic.LoadUint64(&sentCounter) - atomic.LoadUint64(&sentAtSettle)
	steadyWindow := duration - settle
	avgRecv := totalRecv / K
	lossPct := 0.0
	if steadySent > 0 {
		lossPct = (float64(steadySent) - float64(avgRecv)) / float64(steadySent) * 100
	}
	fps := float64(avgRecv) / steadyWindow.Seconds()
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

	return scalabilityStats{
		K:   K,
		min: percentile(allLats, 0), p25: percentile(allLats, 25),
		median: percentile(allLats, 50), p75: percentile(allLats, 75),
		p95: percentile(allLats, 95), p99: percentile(allLats, 99), maxLat: percentile(allLats, 100),
		lossPct: lossPct, fps: fps, mbps: fps * float64(frameSize) * 8 / 1e6,
		heapMB: heapMB, goros: goros, cpuMs: cpu.Seconds() * 1000,
		fairness: fairness,
	}
}

// subscribeAndRead dials a relay, subscribes, reads groups until timeout,
// returns per-group latencies.
func subscribeAndRead(tb testing.TB, addr string, pool *x509.CertPool, timeout time.Duration, settleAt time.Time) []time.Duration {
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
				if time.Now().Before(settleAt) {
					continue // ramp-up sample: excluded from steady-state stats
				}
				pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
				lats = append(lats, time.Since(time.Unix(0, pubNs)))
			}
		}
	}
	return lats
}
