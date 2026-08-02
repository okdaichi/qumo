package ingest

import (
	"net"
	"net/url"
	"strconv"
	"testing"

	"github.com/qumo-dev/qumo/internal/rtsp"
)

// benchStartServer (the testing.TB variant of startRTSPServer) lives in
// rtsp_loop_bench_test.go and is shared across this package's benchmarks — do
// not re-declare it here.

// BenchmarkRTSPConnCycle measures the per-connection accept→handle→teardown
// cost — the accept-loop goroutine path (connWg.Add → go handleConn → Done)
// changed by removing the deferred connWg.Done(). Each iteration opens a fresh
// loopback connection, issues one OPTIONS request, reads the response, and
// closes. Regression/improvement guard for the accept-loop change.
func BenchmarkRTSPConnCycle(b *testing.B) {
	_, addr := benchStartServer(b)
	u, err := url.Parse("rtsp://" + addr.String() + "/bench")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := net.Dial("tcp", addr.String())
		if err != nil {
			b.Fatal(err)
		}
		rc := rtsp.NewConn(c)
		req := &rtsp.Request{
			Method: rtsp.MethodOptions,
			URL:    u,
			Proto:  "RTSP/1.0",
			Header: map[string][]string{},
		}
		req.Header.Set("CSeq", strconv.Itoa(1))
		if err := rc.WriteRequest(req); err != nil {
			b.Fatal(err)
		}
		if _, _, err := rc.ReadResponse(req); err != nil {
			b.Fatal(err)
		}
		_ = c.Close()
	}
}
