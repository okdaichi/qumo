//go:build integration

package integration

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/ffpub"
)

// TestRTSP_PTSDiagnostic is a wide-net regression hunter for #229.
//
// TestRTSPInterop_Matrix/gop60_720p30 intermittently fails the PTS-regression
// gate on CI Linux with a deterministic ~1.97s backward PTS jump at a GOP
// boundary. The matrix test's bounded 3-group window only catches it when OS
// timing happens to land the collector on the anomalous frame pair, and the
// stopgap widened window now masks it entirely — so the anomaly currently goes
// undiagnosed. This test casts a much wider net (many groups) and, if it finds
// any backward PTS jump beyond jitter, FAILS with a full dump of the regression
// site (surrounding frames, deltas, keyframe flags, group count) so the next CI
// run on Linux yields the data needed to root-cause #229.
//
// On environments where the stream is well-behaved (e.g. Windows: ~2800 frames
// observed with zero regressions), this passes and is a no-op.
func TestRTSP_PTSDiagnostic(t *testing.T) {
	if !ffpub.Available() {
		t.Skip("ffmpeg not on PATH")
	}

	mux := moqt.NewTrackMux(0)
	rtspAddr, serveURL := setupRTSPPipeline(t, mux)

	const path = "/live/ptsdiag"
	pubCtx, cancelPub := context.WithCancel(context.Background())
	defer cancelPub()

	pub := ffpub.New(ffpub.Config{
		URL:       fmt.Sprintf("rtsp://%s%s", rtspAddr, path),
		GOP:       60, Width: 1280, Height: 720, Framerate: 30,
	})
	if err := pub.Start(pubCtx); err != nil {
		t.Fatalf("publish: %v", err)
	}
	t.Cleanup(func() { _ = pub.Wait() })

	// Let ffmpeg ANNOUNCE/SETUP/RECORD and publish a few GOPs before subscribing.
	time.Sleep(2 * time.Second)

	collectCtx, cancelCollect := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancelCollect()
	obs, err := (&Collector{
		Dialer:       &moqt.Dialer{TLSConfig: subscriberTLS(t)},
		URL:          serveURL,
		Path:         moqt.BroadcastPath(path),
		MaxGroups:    30,
		GroupTimeout: 4 * time.Second,
	}).Collect(collectCtx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	cancelPub()

	vo := obs.Tracks["video"]
	if vo == nil {
		t.Fatalf("no video track; order=%v", obs.Order)
	}
	frames := vo.Frames
	t.Logf("collected %d video frames across %d groups", len(frames), vo.GroupCount)

	// Characterize normal inter-frame spacing (median forward delta) so the
	// anomaly's magnitude is reported relative to the stream's real cadence.
	var forwardDeltas []int64
	for i := 1; i < len(frames); i++ {
		if d := frames[i].PTSUS - frames[i-1].PTSUS; d > 0 {
			forwardDeltas = append(forwardDeltas, d)
		}
	}
	median := medianInt64(forwardDeltas)
	t.Logf("median forward PTS delta: %d us (%.1f fps equivalent)", median, 1e6/float64(median))

	// A no-B-frame stream (-bf 0 + tune zerolatency) must be monotonic in decode
	// order. Allow a small jitter tolerance for encoder/clock quantization.
	const jitterUS = 50_000 // 50ms
	type regression struct {
		index                                          int
		prevPTS, curPTS, delta                         int64
		prevKey, curKey                                bool
	}
	var regressions []regression
	for i := 1; i < len(frames); i++ {
		prev, cur := frames[i-1].PTSUS, frames[i].PTSUS
		if delta := cur - prev; delta < -jitterUS {
			regressions = append(regressions, regression{i, prev, cur, delta, frames[i-1].IsKeyframe, frames[i].IsKeyframe})
		}
	}

	if len(regressions) == 0 {
		t.Logf("no PTS regression > %d us detected — stream is monotonic in this window", jitterUS)
		return
	}

	// Dump every regression with surrounding context. This is the diagnostic
	// payload that makes #229 root-causeable on CI.
	out := fmt.Sprintf("DETECTED %d PTS regression(s) > %d us (median forward delta %d us):\n",
		len(regressions), jitterUS, median)
	for _, r := range regressions {
		start := r.index - 3
		if start < 0 {
			start = 0
		}
		end := r.index + 3
		if end > len(frames) {
			end = len(frames)
		}
		out += fmt.Sprintf("\n  regression @ frame %d: pts=%d -> %d (delta=%d us, %.3fs); prevKey=%v curKey=%v\n",
			r.index, r.prevPTS, r.curPTS, r.delta, float64(-r.delta)/1e6, r.prevKey, r.curKey)
		for j := start; j < end; j++ {
			marker := "  "
			if j == r.index {
				marker = ">>"
			}
			d := int64(0)
			if j > 0 {
				d = frames[j].PTSUS - frames[j-1].PTSUS
			}
			out += fmt.Sprintf("    %s frame %d: pts=%-12d delta=%-10d key=%v\n",
				marker, j, frames[j].PTSUS, d, frames[j].IsKeyframe)
		}
	}
	t.Fatalf("%s", out)
}

func medianInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[len(xs)/2]
}
