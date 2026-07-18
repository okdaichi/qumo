//go:build integration

// Multi-track fan-out: N independent publisher→track→subscriber groups share ONE
// relay. Isolates whether the relay's ~63K fps ceiling is per-track (e.g. single
// ingest serialization) or global/shared.
//
// totalSubs subscribers are split round-robin across N tracks; each track has its
// own publisher (its own ingest path). Total egress work is held constant while
// the number of parallel ingest paths varies. If aggregate delivered rises with N
// (past the single-track ~63K), single-ingest serialization is implicated; if it
// stays flat, the cap is global and ingest is ruled out.
//
// Env: TRACKS=N list (default 1,2,4,8), TOTAL_SUBS (default 128),
// FRAMES_PER_GROUP/FRAME_GAP_MS (per-publisher rate), BENCH_DURATION.

package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/require"
)

func BenchmarkRelay_MultiTrack(b *testing.B) {
	cert, pool := chainCert(b)
	quicCfg := &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, MaxIncomingUniStreams: 1 << 20, MaxIncomingStreams: 1 << 20}

	dur := 6 * time.Second
	if d := os.Getenv("BENCH_DURATION"); d != "" {
		if p, err := time.ParseDuration(d); err == nil {
			dur = p
		}
	}
	totalSubs := envIntDef("TOTAL_SUBS", 128)
	framesPerGroup := envIntDef("FRAMES_PER_GROUP", 250)
	frameGapMs := envIntDef("FRAME_GAP_MS", 1)
	ns := parseIntListEnv("TRACKS", []int{1, 2, 4, 8})
	const frameSize = 1200

	log.Printf("\n=== Multi-Track (totalSubs=%d, frames/group=%d, frameGap=%dms, dur=%s, tracks=%v) ===",
		totalSubs, framesPerGroup, frameGapMs, dur, ns)
	log.Printf("%-8s %-10s %-12s %-12s %-10s", "tracks", "subs/track", "agg_target", "agg_deliv", "per_track")

	for _, N := range ns {
		b.Run(fmt.Sprintf("N=%d", N), func(b *testing.B) {
			agg := multiTrackRun(b, cert, pool, quicCfg, N, totalSubs, frameSize, framesPerGroup, time.Duration(frameGapMs)*time.Millisecond, dur)
			b.ReportMetric(agg, "agg_fps")
			subsPerTrack := totalSubs / N
			// publisher rate ≈ 1000/frameGapMs fps; per-track target = rate × subs/track
			rate := 1000.0 / float64(frameGapMs)
			aggTarget := rate * float64(totalSubs)
			perTrack := agg / float64(N)
			log.Printf("%-8d %-10d %-12.0f %-12.0f %-10.0f", N, subsPerTrack, aggTarget, agg, perTrack)
		})
	}
}

func multiTrackRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, quicCfg *quic.Config, N, totalSubs, frameSize, framesPerGroup int, frameGap, duration time.Duration) float64 {
	tb.Helper()
	relay := spinRelay(tb, "relay", chainFreeAddr(tb), cert, pool, quicCfg)
	relayAddr := relay.MOQServer.Addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, duration)
	defer runCancel()

	paths := make([]moqt.BroadcastPath, N)
	for i := range paths {
		paths[i] = moqt.BroadcastPath(fmt.Sprintf("/bench/mt%d", i))
	}

	// N publishers, each on its own session (independent ingest path).
	var pubSessions []*moqt.Session
	for i := 0; i < N; i++ {
		i := i
		mux := moqt.NewTrackMux(moqt.NewHopID())
		mux.PublishFunc(runCtx, paths[i], func(tw *moqt.TrackWriter) {
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
						_ = gw.Close()
						return
					}
					binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
					fr := moqt.NewFrame(frameSize)
					_, _ = fr.Write(payload)
					_ = gw.WriteFrame(fr)
					if frameGap > 0 {
						time.Sleep(frameGap)
					}
				}
				_ = gw.Close()
			}
		})
		sess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(runCtx, "moqt://"+relayAddr, mux)
		require.NoError(tb, err)
		pubSessions = append(pubSessions, sess)
	}
	defer func() {
		for _, s := range pubSessions {
			_ = s.CloseWithError(moqt.NoError, "done")
		}
	}()

	// Wait for all N handlers to register on the relay.
	deadline := time.Now().Add(15 * time.Second)
	for _, p := range paths {
		for time.Now().Before(deadline) {
			if ann, _ := relay.TrackMux.TrackHandler(p); ann != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// totalSubs subscribers, round-robin across the N tracks.
	var totalRecv atomic.Uint64
	var subWG sync.WaitGroup
	subWG.Add(totalSubs)
	for j := 0; j < totalSubs; j++ {
		j := j
		go func() {
			defer subWG.Done()
			n := subscribeAndCountFrames(tb, relayAddr, pool, quicCfg, paths[j%N], duration+6*time.Second)
			totalRecv.Add(uint64(n))
		}()
	}
	subWG.Wait()

	return float64(totalRecv.Load()) / duration.Seconds()
}

// subscribeAndCountFrames dials, subscribes to path, and counts frames received
// until the context expires. Returns the frame count.
func subscribeAndCountFrames(tb testing.TB, addr string, pool *x509.CertPool, quicCfg *quic.Config, path moqt.BroadcastPath, timeout time.Duration) int {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, err := (&moqt.Dialer{TLSConfig: chainDialerTLS(pool), QUICConfig: quicCfg}).Dial(ctx, "moqt://"+addr, moqt.NewTrackMux(0))
	if err != nil {
		return 0
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	tr, err := sess.Subscribe(ctx, path, chainTrackName, nil)
	if err != nil {
		return 0
	}
	defer tr.Close()
	buf := moqt.NewFrame(1200 + 256)
	var count int
	for {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			break
		}
		for frame := range gr.Frames(buf) {
			_ = frame
			count++
			if count == math.MaxInt32 {
				return count
			}
		}
	}
	return count
}
