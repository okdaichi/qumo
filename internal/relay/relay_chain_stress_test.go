//go:build integration

// Relay-chain stress (availability) + throughput harness. Same in-process QUIC
// relay machinery as relay_chain_bench_test.go; measures the remaining
// decision-grade stats under sustained load:
//
//   - THROUGHPUT (capacity): max sustained frames/sec and Mbps the chain
//     delivers (the gap=0 sub-run) — bounds how many streams / what bitrate a
//     relay tier handles.
//   - AVAILABILITY: frame-loss rate, latency drift (first vs last slice — a
//     growing backlog), memory growth (a leak), and survival (no panic).
//
// Frames are published ONE PER GROUP (a real media stream is group-per-frame)
// rather than many-in-one-group — gomoqt caps a single group at ~256 frames.
//
//	go test -tags=integration -run='TestRelayChain_Stress' -timeout 180s \
//	    ./internal/relay/ -v
package relay

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/require"
)

const (
	stressFrameSize  = 1200
	stressDuration   = 5 * time.Second
	sustainableGap   = 2 * time.Millisecond // lowest rate; asserted loss-free
)

// TestRelayChain_Stress drives sustained load through a depth-1 chain at three
// rates to locate the throughput knee and verify availability below it:
//   - ~500 / 1000 / 2000 fps (2ms / 1ms / 0.5ms inter-frame gap).
//
// For each rate it reports sent, received, loss%, delivered throughput (fps,
// Mbps), p99 latency, latency drift (median last-slice − median first-slice — a
// growing backlog), and heap growth. The lowest rate is asserted LOSS-FREE with
// stable latency (availability regression gate); higher rates locate where loss
// begins (capacity). Rates are bounded (not unbounded flood) to avoid QUIC
// send-buffer blowup and keep the run within timeout.
func TestRelayChain_Stress(t *testing.T) {
	cert, pool := chainCert(t)
	quicCfg := &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 5 * time.Second, MaxIdleTimeout: 30 * time.Second}
	const depth = 1

	for _, gap := range []time.Duration{2 * time.Millisecond, time.Millisecond, 500 * time.Microsecond} {
		t.Run(fmt.Sprintf("gap=%s", gap), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			relays := make([]*Server, depth)
			for i := range depth {
				relays[i] = spinRelay(t, fmt.Sprintf("r%d", i), chainFreeAddr(t), cert, pool, quicCfg)
			}
			for i := 1; i < depth; i++ {
				relays[i].Config.Peers = []Peer{{Address: relays[i-1].MOQServer.Addr}}
				i := i
				go relays[i].ConnectPeers(ctx) //nolint:errcheck
			}

			sent, lats, heapGrowth := stressRun(t, ctx, pool, relays, gap, stressDuration)
			received := len(lats)
			require.NotZero(t, sent, "producer sent nothing")
			require.NotEmpty(t, lats, "received no frames")

			loss := sent - uint64(received)
			lossPct := float64(loss) / float64(sent) * 100
			fps := float64(received) / stressDuration.Seconds()
			mbps := fps * stressFrameSize * 8 / 1e6
			p99 := percentile(lats, 99)
			med := percentile(lats, 50)
			drift := percentile(lastFrac(lats, 0.2), 50) - percentile(firstFrac(lats, 0.2), 50)

			log.Printf("[chain-stress] gap=%-6s sent=%-6d recv=%-6d loss=%-5d (%5.2f%%)  thr=%.0f fps (%.1f Mbps)  lat med=%-7s p99=%-7s drift=%-7s heapΔ=%5.2fMB  survived=✓",
				gap, sent, received, loss, lossPct, fps, mbps,
				med.Round(time.Microsecond), p99.Round(time.Microsecond), drift.Round(time.Microsecond),
				float64(heapGrowth)/(1024*1024))

			// Availability gate: the lowest (sustainable) rate must be loss-free
			// and stable. Higher rates locate the knee and may drop — reported only.
			if gap == sustainableGap {
				require.Equal(t, uint64(0), loss, "frame loss at the sustainable rate (availability regression)")
				require.Less(t, drift, 5*time.Millisecond, "latency drift indicates a growing backlog")
			}
		})
	}
}

// stressRun publishes one group per frame for `duration` at the given inter-frame
// gap (0 = max rate) and returns the count sent, the per-frame latencies
// received, and heap growth over the run.
func stressRun(tb testing.TB, parent context.Context, pool *x509.CertPool, relays []*Server, gap, duration time.Duration) (sent uint64, latencies []time.Duration, heapGrowth uint64) {
	tb.Helper()
	runCtx, runCancel := context.WithTimeout(parent, duration)
	defer runCancel()

	var sentCounter uint64
	pubMux := moqt.NewTrackMux(moqt.NewHopID())
	pubMux.PublishFunc(runCtx, chainBroadcastPath, func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, stressFrameSize)
		for {
			if runCtx.Err() != nil {
				return
			}
			seq := atomic.AddUint64(&sentCounter, 1)
			binary.BigEndian.PutUint64(payload[0:8], seq)
			binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
			gw, err := tw.OpenGroup(runCtx)
			if err != nil {
				return
			}
			fr := moqt.NewFrame(stressFrameSize)
			_, _ = fr.Write(payload)
			if err := gw.WriteFrame(fr); err != nil {
				_ = gw.Close()
				return
			}
			_ = gw.Close()
			if gap > 0 {
				time.Sleep(gap)
			}
		}
	})
	pubSess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool)}).Dial(runCtx, "moqt://"+relays[0].MOQServer.Addr, pubMux)
	require.NoError(tb, err)
	defer pubSess.CloseWithError(moqt.NoError, "done")

	waitForHandler(tb, relays[len(relays)-1], chainBroadcastPath)

	var memBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	subSess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool)}).Dial(runCtx, "moqt://"+relays[len(relays)-1].MOQServer.Addr, moqt.NewTrackMux(0))
	require.NoError(tb, err)
	defer subSess.CloseWithError(moqt.NoError, "done")
	tr, err := subSess.Subscribe(runCtx, chainBroadcastPath, chainTrackName, nil)
	require.NoError(tb, err)
	defer tr.Close()

	// Read groups until the producer stops (runCtx expiry) or a safety timeout.
	readCtx, readCancel := context.WithTimeout(context.Background(), duration+5*time.Second)
	defer readCancel()
	go func() { <-readCtx.Done(); _ = tr.Close() }()

	buf := moqt.NewFrame(stressFrameSize + 256)
	latencies = make([]time.Duration, 0, 8192)
	for {
		gr, err := tr.AcceptGroup(readCtx)
		if err != nil {
			break
		}
		for frame := range gr.Frames(buf) {
			body := frame.Body()
			if len(body) < chainFrameHeader {
				continue
			}
			pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
			latencies = append(latencies, time.Since(time.Unix(0, pubNs)))
		}
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	sent = atomic.LoadUint64(&sentCounter)
	return sent, latencies, memAfter.HeapAlloc - memBefore.HeapAlloc
}

// ---- stat helpers ----

func percentile(xs []time.Duration, p int) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	s := make([]time.Duration, len(xs))
	copy(s, xs)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)*p/100]
}

func firstFrac(xs []time.Duration, frac float64) []time.Duration {
	if len(xs) == 0 {
		return xs
	}
	n := int(math.Floor(float64(len(xs)) * frac))
	if n < 1 {
		n = 1
	}
	return xs[:n]
}

func lastFrac(xs []time.Duration, frac float64) []time.Duration {
	if len(xs) == 0 {
		return xs
	}
	n := int(math.Floor(float64(len(xs)) * frac))
	if n < 1 {
		n = 1
	}
	return xs[len(xs)-n:]
}
